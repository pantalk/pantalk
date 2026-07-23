package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pantalk/pantalk/internal/media"
	"github.com/pantalk/pantalk/internal/protocol"
)

func newTestMediaStore(t *testing.T, maxBytes int64) *media.FSStore {
	t.Helper()

	store, err := media.NewFSStore(t.TempDir(), maxBytes)
	if err != nil {
		t.Fatalf("new media store: %v", err)
	}

	return store
}

func TestTelegramFileCandidatesPicksLargestPhoto(t *testing.T) {
	message := &tgMessage{
		Photo: []tgPhotoSize{
			{FileID: "small", FileSize: 100},
			{FileID: "large", FileSize: 9000},
			{FileID: "medium", FileSize: 2000},
		},
	}

	candidates := telegramFileCandidates(message)
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(candidates))
	}
	if candidates[0].FileID != "large" {
		t.Fatalf("file id = %q, want %q", candidates[0].FileID, "large")
	}
	if candidates[0].MIMEType != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", candidates[0].MIMEType)
	}
}

func TestTelegramFileCandidatesCoversEveryVariant(t *testing.T) {
	message := &tgMessage{
		Document:  &tgFileMeta{FileID: "doc", FileName: "report.pdf", MIMEType: "application/pdf"},
		Audio:     &tgFileMeta{FileID: "audio"},
		Video:     &tgFileMeta{FileID: "video"},
		Voice:     &tgFileMeta{FileID: "voice"},
		VideoNote: &tgFileMeta{FileID: "videonote"},
		Animation: &tgFileMeta{FileID: "animation"},
		Sticker:   &tgFileMeta{FileID: "sticker"},
	}

	candidates := telegramFileCandidates(message)
	if len(candidates) != 7 {
		t.Fatalf("got %d candidates, want 7", len(candidates))
	}
	if candidates[0].FileName != "report.pdf" {
		t.Fatalf("first candidate = %+v", candidates[0])
	}
}

func TestTelegramFileCandidatesIgnoresEmpty(t *testing.T) {
	if got := telegramFileCandidates(nil); got != nil {
		t.Fatalf("nil message produced %+v", got)
	}
	if got := telegramFileCandidates(&tgMessage{Text: "no files"}); got != nil {
		t.Fatalf("text-only message produced %+v", got)
	}
	if got := telegramFileCandidates(&tgMessage{Photo: []tgPhotoSize{{FileID: ""}}}); got != nil {
		t.Fatalf("blank photo id produced %+v", got)
	}
}

func TestIsTelegramInlineImage(t *testing.T) {
	tests := map[string]bool{
		"photo.jpg":  true,
		"photo.JPEG": true,
		"shot.png":   true,
		"art.webp":   true,
		"clip.gif":   false, // sendAnimation territory
		"vector.svg": false,
		"report.pdf": false,
		"noext":      false,
	}

	for name, want := range tests {
		if got := isTelegramInlineImage(name); got != want {
			t.Errorf("isTelegramInlineImage(%q) = %v, want %v", name, got, want)
		}
	}
}

// newTelegramFileServer stands up a fake Bot API exposing getFile plus the file
// download endpoint, returning the payload it serves.
func newTelegramFileServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bottest-token/getFile", func(w http.ResponseWriter, r *http.Request) {
		fileID := r.URL.Query().Get("file_id")
		if fileID == "" {
			http.Error(w, "missing file_id", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(tgGetFileResponse{
			OK:     true,
			Result: tgFile{FileID: fileID, FilePath: "photos/file_1.jpg"},
		})
	})
	mux.HandleFunc("/file/bottest-token/photos/file_1.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, payload)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestTelegramDownloadsInboundAttachment(t *testing.T) {
	payload := "fake-jpeg-bytes"
	srv := newTelegramFileServer(t, payload)
	store := newTestMediaStore(t, 1024)

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		fileBaseURL: srv.URL + "/file/bottest-token",
		httpClient:  srv.Client(),
		attachments: store,
		channels:    map[string]struct{}{},
	}

	message := &tgMessage{
		Photo: []tgPhotoSize{{FileID: "AgACAgQ", FileSize: int64(len(payload))}},
	}

	attachments := connector.collectAttachments(context.Background(), message)
	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(attachments))
	}

	sum := sha256.Sum256([]byte(payload))
	wantDigest := hex.EncodeToString(sum[:])

	got := attachments[0]
	if got.Digest != wantDigest {
		t.Fatalf("digest = %q, want %q", got.Digest, wantDigest)
	}
	if got.RemoteID != "AgACAgQ" {
		t.Fatalf("remote id = %q, want AgACAgQ", got.RemoteID)
	}
	if got.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", got.Size, len(payload))
	}

	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read stored attachment: %v", err)
	}
	if string(stored) != payload {
		t.Fatalf("stored bytes = %q, want %q", stored, payload)
	}
}

// With storage disabled the message must still report that a file arrived.
func TestTelegramRecordsMetadataWhenStorageDisabled(t *testing.T) {
	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	message := &tgMessage{
		Document: &tgFileMeta{FileID: "doc-1", FileName: "spec.pdf", MIMEType: "application/pdf", FileSize: 4096},
	}

	attachments := connector.collectAttachments(context.Background(), message)
	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(attachments))
	}

	got := attachments[0]
	if got.Digest != "" || got.Path != "" {
		t.Fatalf("expected no stored bytes, got digest=%q path=%q", got.Digest, got.Path)
	}
	if got.Name != "spec.pdf" || got.MIME != "application/pdf" || got.Size != 4096 {
		t.Fatalf("metadata lost: %+v", got)
	}
	if got.RemoteID != "doc-1" {
		t.Fatalf("remote id = %q, want doc-1", got.RemoteID)
	}
}

// A download failure must degrade to metadata rather than dropping the file.
func TestTelegramAttachmentSurvivesDownloadFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bottest-token/getFile", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		fileBaseURL: srv.URL + "/file/bottest-token",
		httpClient:  srv.Client(),
		attachments: newTestMediaStore(t, 1024),
		channels:    map[string]struct{}{},
	}

	attachments := connector.collectAttachments(context.Background(), &tgMessage{
		Document: &tgFileMeta{FileID: "doc-1", FileName: "spec.pdf"},
	})

	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(attachments))
	}
	if attachments[0].Digest != "" {
		t.Fatalf("expected no digest after failed download, got %q", attachments[0].Digest)
	}
	if attachments[0].Name != "spec.pdf" {
		t.Fatalf("metadata lost after failure: %+v", attachments[0])
	}
}

// An attachment larger than the cap is skipped without killing the message.
func TestTelegramOversizedAttachmentDegradesToMetadata(t *testing.T) {
	srv := newTelegramFileServer(t, strings.Repeat("x", 500))

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		fileBaseURL: srv.URL + "/file/bottest-token",
		httpClient:  srv.Client(),
		attachments: newTestMediaStore(t, 16),
		channels:    map[string]struct{}{},
	}

	attachments := connector.collectAttachments(context.Background(), &tgMessage{
		Photo: []tgPhotoSize{{FileID: "big", FileSize: 500}},
	})

	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(attachments))
	}
	if attachments[0].Digest != "" {
		t.Fatalf("oversized file was stored: %+v", attachments[0])
	}
	if attachments[0].RemoteID != "big" {
		t.Fatalf("remote id lost: %+v", attachments[0])
	}
}

// capturedUpload records what the fake Bot API received for an upload.
type capturedUpload struct {
	method   string
	fields   map[string]string
	fileName string
	fileBody string
}

func newTelegramUploadServer(t *testing.T, captured *capturedUpload) *httptest.Server {
	t.Helper()

	handler := func(method string, field string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			captured.method = method
			captured.fields = map[string]string{}
			for key, values := range r.MultipartForm.Value {
				if len(values) > 0 {
					captured.fields[key] = values[0]
				}
			}

			file, header, err := r.FormFile(field)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()

			body, err := io.ReadAll(file)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			captured.fileName = header.Filename
			captured.fileBody = string(body)

			_ = json.NewEncoder(w).Encode(tgSendMessageResponse{
				OK: true,
				Result: tgMessage{
					MessageID: 42,
					Date:      1700000000,
					Chat:      tgChat{ID: -100123},
				},
			})
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/bottest-token/sendPhoto", handler("sendPhoto", "photo"))
	mux.HandleFunc("/bottest-token/sendDocument", handler("sendDocument", "document"))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestTelegramSendsImageViaSendPhoto(t *testing.T) {
	var captured capturedUpload
	srv := newTelegramUploadServer(t, &captured)

	imagePath := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("write test image: %v", err)
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	event, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Text:    "here you go",
		Attach:  []string{imagePath},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if captured.method != "sendPhoto" {
		t.Fatalf("method = %q, want sendPhoto", captured.method)
	}
	if captured.fileName != "shot.png" {
		t.Fatalf("file name = %q, want shot.png", captured.fileName)
	}
	if captured.fileBody != "png-bytes" {
		t.Fatalf("file body = %q", captured.fileBody)
	}
	if captured.fields["caption"] != "here you go" {
		t.Fatalf("caption = %q", captured.fields["caption"])
	}
	if captured.fields["chat_id"] != "-100123" {
		t.Fatalf("chat_id = %q", captured.fields["chat_id"])
	}
	if len(event.Attachments) != 1 || event.Attachments[0].Name != "shot.png" {
		t.Fatalf("returned event attachments = %+v", event.Attachments)
	}
	if event.Direction != "out" {
		t.Fatalf("direction = %q, want out", event.Direction)
	}
}

func TestTelegramSendsNonImageViaSendDocument(t *testing.T) {
	var captured capturedUpload
	srv := newTelegramUploadServer(t, &captured)

	docPath := filepath.Join(t.TempDir(), "notes.pdf")
	if err := os.WriteFile(docPath, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatalf("write test doc: %v", err)
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	if _, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Attach:  []string{docPath},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if captured.method != "sendDocument" {
		t.Fatalf("method = %q, want sendDocument", captured.method)
	}
	if _, hasCaption := captured.fields["caption"]; hasCaption {
		t.Fatalf("empty text should not set a caption, got %q", captured.fields["caption"])
	}
}

// A caption must not be repeated under every file in a multi-attachment send.
func TestTelegramCaptionsOnlyFirstAttachment(t *testing.T) {
	var captured capturedUpload
	srv := newTelegramUploadServer(t, &captured)

	dir := t.TempDir()
	first := filepath.Join(dir, "one.pdf")
	second := filepath.Join(dir, "two.pdf")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	if _, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Text:    "two files",
		Attach:  []string{first, second},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	// captured holds the last upload, which must be uncaptioned.
	if _, hasCaption := captured.fields["caption"]; hasCaption {
		t.Fatalf("second attachment carried a caption: %q", captured.fields["caption"])
	}
	if captured.fileName != "two.pdf" {
		t.Fatalf("last file = %q, want two.pdf", captured.fileName)
	}
}

func TestTelegramSendRejectsMissingAttachment(t *testing.T) {
	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     "http://127.0.0.1:1/bottest-token",
		httpClient:  http.DefaultClient,
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Attach:  []string{filepath.Join(t.TempDir(), "does-not-exist.png")},
	})
	if err == nil {
		t.Fatal("expected an error for a missing attachment")
	}
	if !strings.Contains(err.Error(), "open attachment") {
		t.Fatalf("error = %v, want an open attachment failure", err)
	}
}

// Sending with neither text nor attachments stays an error.
func TestTelegramSendStillRequiresContent(t *testing.T) {
	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	if _, err := connector.Send(context.Background(), protocol.Request{Channel: "-100123"}); err == nil {
		t.Fatal("expected an error when both text and attachments are empty")
	}
}

func TestTelegramTypingSendsChatAction(t *testing.T) {
	var captured tgSendChatActionRequest
	var calls int

	mux := http.NewServeMux()
	mux.HandleFunc("/bottest-token/sendChatAction", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	err := connector.Typing(context.Background(), protocol.Request{
		Channel: "-100123",
		Thread:  "42",
	})
	if err != nil {
		t.Fatalf("typing: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (one call = one pulse; cadence belongs to the daemon)", calls)
	}
	if captured.ChatID != "-100123" {
		t.Fatalf("chat_id = %q", captured.ChatID)
	}
	if captured.Action != "typing" {
		t.Fatalf("action = %q, want typing", captured.Action)
	}
	if captured.MessageThreadID != 42 {
		t.Fatalf("message_thread_id = %d, want 42", captured.MessageThreadID)
	}
}

func TestTelegramTypingRequiresDestination(t *testing.T) {
	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	if err := connector.Typing(context.Background(), protocol.Request{}); err == nil {
		t.Fatal("expected error without a destination")
	}
}

func TestTelegramTypingSurfacesAPIFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bottest-token/sendChatAction", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	if err := connector.Typing(context.Background(), protocol.Request{Channel: "1"}); err == nil {
		t.Fatal("expected error on API failure")
	}
}

// A photo over the sendPhoto limit but under the document limit must fall
// back to sendDocument, matching Telegram's own guidance - not fail.
func TestTelegramOversizedPhotoFallsBackToDocument(t *testing.T) {
	var captured capturedUpload
	srv := newTelegramUploadServer(t, &captured)

	imagePath := filepath.Join(t.TempDir(), "huge.png")
	if err := os.WriteFile(imagePath, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	connector := &TelegramConnector{
		serviceName:      "telegram",
		botName:          "test",
		baseURL:          srv.URL + "/bottest-token",
		httpClient:       srv.Client(),
		publish:          func(protocol.Event) {},
		attachments:      media.NoopStore{},
		channels:         map[string]struct{}{},
		maxPhotoBytes:    4,    // the 10-byte "photo" is over this
		maxDocumentBytes: 1024, // but comfortably under this
	}

	if _, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Attach:  []string{imagePath},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if captured.method != "sendDocument" {
		t.Fatalf("method = %q, want sendDocument fallback", captured.method)
	}
}

// A file over the document limit must fail before any bytes are streamed.
func TestTelegramRejectsFileOverDocumentLimit(t *testing.T) {
	uploaded := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		uploaded = true
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	docPath := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(docPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	connector := &TelegramConnector{
		serviceName:      "telegram",
		botName:          "test",
		baseURL:          srv.URL + "/bottest-token",
		httpClient:       srv.Client(),
		publish:          func(protocol.Event) {},
		attachments:      media.NoopStore{},
		channels:         map[string]struct{}{},
		maxDocumentBytes: 4,
	}

	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Attach:  []string{docPath},
	})
	if err == nil || !strings.Contains(err.Error(), "upload limit") {
		t.Fatalf("err = %v, want an upload limit rejection", err)
	}
	if uploaded {
		t.Fatal("bytes were streamed despite the pre-check")
	}
}

func TestTelegramRejectsEmptyAttachment(t *testing.T) {
	emptyPath := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Attach:  []string{emptyPath},
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want empty-file rejection", err)
	}
}

// Captions over Telegram's 1024 limit fail before any upload starts.
func TestTelegramRejectsOverlongCaption(t *testing.T) {
	uploaded := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		uploaded = true
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	docPath := filepath.Join(t.TempDir(), "notes.pdf")
	if err := os.WriteFile(docPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		baseURL:     srv.URL + "/bottest-token",
		httpClient:  srv.Client(),
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Text:    strings.Repeat("x", 1025),
		Attach:  []string{docPath},
	})
	if err == nil || !strings.Contains(err.Error(), "caption") {
		t.Fatalf("err = %v, want caption rejection", err)
	}
	if uploaded {
		t.Fatal("upload happened despite overlong caption")
	}
}

// Telegram counts caption length in UTF-16 code units: 513 astral-plane emoji
// are 513 runes but 1026 units, and must be rejected. This pins the counting
// choice - a rune count would wrongly accept this caption.
func TestTelegramCaptionLengthCountsUTF16Units(t *testing.T) {
	emoji := strings.Repeat("\U0001F600", 513) // 513 runes, 1026 UTF-16 units

	if got := telegramTextLength(emoji); got != 1026 {
		t.Fatalf("telegramTextLength = %d, want 1026", got)
	}

	docPath := filepath.Join(t.TempDir(), "a.pdf")
	if err := os.WriteFile(docPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	connector := &TelegramConnector{
		serviceName: "telegram",
		botName:     "test",
		publish:     func(protocol.Event) {},
		attachments: media.NoopStore{},
		channels:    map[string]struct{}{},
	}

	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "-100123",
		Text:    emoji,
		Attach:  []string{docPath},
	})
	if err == nil || !strings.Contains(err.Error(), "caption") {
		t.Fatalf("err = %v, want caption rejection for emoji caption", err)
	}
}
