package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/formatting"
	"github.com/pantalk/pantalk/internal/media"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultTelegramEndpoint = "https://api.telegram.org"

// Telegram Bot API upload limits. Checked before any bytes are streamed so
// the caller gets an actionable error (or an automatic fallback) instead of a
// bare "status 400" after uploading the whole file.
const (
	telegramMaxPhotoBytes    = 10 << 20 // sendPhoto rejects photos over 10 MiB
	telegramMaxDocumentBytes = 50 << 20 // sendDocument rejects files over 50 MiB
	telegramMaxCaptionChars  = 1024     // caption limit, in UTF-16 code units
)

type TelegramConnector struct {
	serviceName string
	botName     string
	baseURL     string
	fileBaseURL string
	token       string
	publish     func(protocol.Event)
	httpClient  *http.Client
	attachments media.Store

	// Upload limit overrides; zero means the Bot API defaults. Instance
	// fields rather than package variables so tests can shrink them without
	// racing other tests.
	maxPhotoBytes    int64
	maxDocumentBytes int64

	mu           sync.RWMutex
	channels     map[string]struct{}
	selfBotID    int64
	nextUpdateID int64
}

func (t *TelegramConnector) photoLimit() int64 {
	if t.maxPhotoBytes > 0 {
		return t.maxPhotoBytes
	}
	return telegramMaxPhotoBytes
}

func (t *TelegramConnector) documentLimit() int64 {
	if t.maxDocumentBytes > 0 {
		return t.maxDocumentBytes
	}
	return telegramMaxDocumentBytes
}

type tgGetMeResponse struct {
	OK     bool      `json:"ok"`
	Result tgBotUser `json:"result"`
}

type tgBotUser struct {
	ID int64 `json:"id"`
}

type tgGetUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type tgGetUpdatesResponse struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID          int64      `json:"update_id"`
	Message           *tgMessage `json:"message,omitempty"`
	EditedMessage     *tgMessage `json:"edited_message,omitempty"`
	ChannelPost       *tgMessage `json:"channel_post,omitempty"`
	EditedChannelPost *tgMessage `json:"edited_channel_post,omitempty"`
}

type tgMessage struct {
	MessageID       int64      `json:"message_id"`
	Date            int64      `json:"date"`
	Text            string     `json:"text"`
	Caption         string     `json:"caption"`
	Chat            tgChat     `json:"chat"`
	From            *tgUser    `json:"from,omitempty"`
	MessageThreadID int64      `json:"message_thread_id,omitempty"`
	ReplyToMessage  *tgMessage `json:"reply_to_message,omitempty"`

	// Attachment variants. Telegram sends exactly one of these per message,
	// except Photo which arrives as a set of pre-scaled sizes.
	Photo     []tgPhotoSize `json:"photo,omitempty"`
	Document  *tgFileMeta   `json:"document,omitempty"`
	Audio     *tgFileMeta   `json:"audio,omitempty"`
	Video     *tgFileMeta   `json:"video,omitempty"`
	Voice     *tgFileMeta   `json:"voice,omitempty"`
	VideoNote *tgFileMeta   `json:"video_note,omitempty"`
	Animation *tgFileMeta   `json:"animation,omitempty"`
	Sticker   *tgFileMeta   `json:"sticker,omitempty"`
}

// tgFileMeta covers the shared shape of Telegram's file-bearing types. Not
// every variant populates every field - voice notes have no file_name, and
// stickers have no mime_type - so all of them are optional.
type tgFileMeta struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type tgPhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type tgGetFileResponse struct {
	OK     bool   `json:"ok"`
	Result tgFile `json:"result"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgUser struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

type tgSendMessageRequest struct {
	ChatID           string `json:"chat_id"`
	Text             string `json:"text"`
	ParseMode        string `json:"parse_mode,omitempty"`
	MessageThreadID  int64  `json:"message_thread_id,omitempty"`
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
}

type tgSendMessageResponse struct {
	OK     bool      `json:"ok"`
	Result tgMessage `json:"result"`
}

type telegramOutboundSegment struct {
	Text      string
	ParseMode string
}

func NewTelegramConnector(bot config.BotConfig, publish func(protocol.Event), attachments media.Store) (*TelegramConnector, error) {
	token, err := config.ResolveCredential(bot.BotToken)
	if err != nil {
		return nil, fmt.Errorf("resolve telegram bot_token for bot %q: %w", bot.Name, err)
	}

	endpoint := strings.TrimSpace(bot.Endpoint)
	if endpoint == "" {
		endpoint = defaultTelegramEndpoint
	}

	if attachments == nil {
		attachments = media.NoopStore{}
	}

	root := strings.TrimRight(endpoint, "/")

	connector := &TelegramConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		baseURL:     root + "/bot" + token,
		fileBaseURL: root + "/file/bot" + token,
		token:       token,
		publish:     publish,
		httpClient:  &http.Client{Timeout: 70 * time.Second},
		attachments: attachments,
		channels:    make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		connector.channels[trimmed] = struct{}{}
	}

	return connector, nil
}

func (t *TelegramConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			t.publishStatus("connector offline")
			return
		default:
		}

		if err := t.loadSelf(ctx); err != nil {
			log.Printf("[telegram:%s] auth failed: %v", t.botName, err)
			t.publishStatus("telegram auth failed: " + err.Error())
			t.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		log.Printf("[telegram:%s] authenticated (bot_id=%d)", t.botName, t.selfBotID)
		t.resolveChannelNames(ctx)
		t.publishStatus("connector online")
		t.pollLoop(ctx)
	}
}

func (t *TelegramConnector) pollLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates(ctx)
		if err != nil {
			t.publishStatus("telegram getUpdates error: " + err.Error())
			t.sleepOrDone(ctx, 2*time.Second)
			continue
		}

		for _, update := range updates {
			t.advanceOffset(update.UpdateID + 1)
			message := selectTelegramMessage(update)
			if message == nil {
				continue
			}

			if t.isSelfMessage(message) {
				continue
			}

			channelID := strconv.FormatInt(message.Chat.ID, 10)
			direct := message.Chat.Type == "private"
			if !direct && !t.acceptsChannel(channelID) {
				continue
			}

			text := strings.TrimSpace(message.Text)
			if text == "" {
				text = strings.TrimSpace(message.Caption)
			}

			thread := ""
			if message.MessageThreadID > 0 {
				thread = strconv.FormatInt(message.MessageThreadID, 10)
			} else if message.ReplyToMessage != nil && message.ReplyToMessage.MessageID > 0 {
				thread = strconv.FormatInt(message.ReplyToMessage.MessageID, 10)
			}

			userID := ""
			if message.From != nil {
				userID = strconv.FormatInt(message.From.ID, 10)
			}

			// A photo or document with no caption yields an empty text; the
			// attachment is the payload in that case.
			attachments := t.collectAttachments(ctx, message)

			t.publish(protocol.Event{
				Timestamp:   time.Unix(message.Date, 0).UTC(),
				Service:     t.serviceName,
				Bot:         t.botName,
				Kind:        "message",
				Direction:   "in",
				User:        userID,
				Target:      "chat:" + channelID,
				Channel:     channelID,
				Thread:      thread,
				Direct:      direct,
				Text:        text,
				Attachments: attachments,
			})
		}
	}
}

func (t *TelegramConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" && len(request.Attach) == 0 {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	chatID := resolveTelegramChat(request)
	if chatID == "" {
		return protocol.Event{}, fmt.Errorf("telegram send requires channel or target")
	}
	t.rememberChannel(chatID)

	// Attachments are delivered first, with the message text riding along as
	// the caption of the first file so a captioned upload stays a single
	// Telegram message rather than a file followed by a stray comment.
	if len(request.Attach) > 0 {
		return t.sendAttachments(ctx, request, chatID)
	}

	segments, err := prepareTelegramSegments(request.Format, request.Text)
	if err != nil {
		return protocol.Event{}, err
	}

	if len(segments) == 0 {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	var lastEvent protocol.Event
	for _, segment := range segments {
		payload := tgSendMessageRequest{ChatID: chatID, Text: segment.Text, ParseMode: segment.ParseMode}
		if request.Thread != "" {
			if threadID, parseErr := strconv.ParseInt(request.Thread, 10, 64); parseErr == nil {
				payload.ReplyToMessageID = threadID
			}
		}

		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return protocol.Event{}, marshalErr
		}

		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/sendMessage", bytes.NewReader(body))
		if reqErr != nil {
			return protocol.Event{}, reqErr
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, doErr := t.httpClient.Do(httpReq)
		if doErr != nil {
			return protocol.Event{}, doErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return protocol.Event{}, fmt.Errorf("telegram sendMessage failed: status %d", resp.StatusCode)
		}

		var sendResponse tgSendMessageResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&sendResponse)
		resp.Body.Close()
		if decodeErr != nil {
			return protocol.Event{}, decodeErr
		}
		if !sendResponse.OK {
			return protocol.Event{}, fmt.Errorf("telegram sendMessage returned not ok")
		}

		channel := strconv.FormatInt(sendResponse.Result.Chat.ID, 10)
		thread := request.Thread
		if thread == "" && sendResponse.Result.MessageThreadID > 0 {
			thread = strconv.FormatInt(sendResponse.Result.MessageThreadID, 10)
		}

		target := request.Target
		if target == "" {
			target = "chat:" + channel
		}

		event := protocol.Event{
			Timestamp: time.Unix(sendResponse.Result.Date, 0).UTC(),
			Service:   t.serviceName,
			Bot:       t.botName,
			Kind:      "message",
			Direction: "out",
			User:      t.Identity(),
			Target:    target,
			Channel:   channel,
			Thread:    thread,
			Text:      segment.Text,
		}
		t.publish(event)
		lastEvent = event
	}

	return lastEvent, nil
}

// sendAttachments uploads each local file in request.Attach as its own
// Telegram message. Images go out via sendPhoto so they render inline; every
// other type uses sendDocument, which preserves the bytes exactly.
func (t *TelegramConnector) sendAttachments(ctx context.Context, request protocol.Request, chatID string) (protocol.Event, error) {
	caption := strings.TrimSpace(request.Text)

	// Pre-check the caption before any bytes move: Telegram counts caption
	// length in UTF-16 code units and rejects the whole upload past 1024.
	if length := telegramTextLength(caption); length > telegramMaxCaptionChars {
		return protocol.Event{}, fmt.Errorf("caption is %d characters, over Telegram's limit of %d - shorten it or send the text as a separate message", length, telegramMaxCaptionChars)
	}

	var lastEvent protocol.Event
	for index, rawPath := range request.Attach {
		cleanPath := strings.TrimSpace(rawPath)
		if cleanPath == "" {
			continue
		}

		// Only the first upload carries the caption, so a multi-file send does
		// not repeat the same text under every attachment.
		fileCaption := ""
		if index == 0 {
			fileCaption = caption
		}

		event, err := t.uploadTelegramFile(ctx, request, chatID, cleanPath, fileCaption)
		if err != nil {
			return protocol.Event{}, err
		}

		t.publish(event)
		lastEvent = event
	}

	if lastEvent.Service == "" {
		return protocol.Event{}, fmt.Errorf("no readable attachments in request")
	}

	return lastEvent, nil
}

// uploadTelegramFile posts a single local file as multipart/form-data.
func (t *TelegramConnector) uploadTelegramFile(ctx context.Context, request protocol.Request, chatID string, filePath string, caption string) (protocol.Event, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("open attachment %q: %w", filePath, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return protocol.Event{}, fmt.Errorf("stat attachment %q: %w", filePath, err)
	}
	if info.IsDir() {
		return protocol.Event{}, fmt.Errorf("attachment %q is a directory", filePath)
	}

	baseName := filepath.Base(filePath)
	size := info.Size()

	// Pre-check sizes against the Bot API limits so failures are actionable
	// and cost nothing - without this, a too-large file streams completely
	// before Telegram answers with an unexplained 400.
	if size == 0 {
		return protocol.Event{}, fmt.Errorf("attachment %q is empty", filePath)
	}
	if size > t.documentLimit() {
		return protocol.Event{}, fmt.Errorf("attachment %q is %d bytes, over Telegram's upload limit of %d", filePath, size, t.documentLimit())
	}

	method, field := "sendDocument", "document"
	if isTelegramInlineImage(baseName) {
		if size <= t.photoLimit() {
			method, field = "sendPhoto", "photo"
		} else {
			// Telegram's own guidance for photos over the sendPhoto limit:
			// deliver as a document. The image arrives byte-exact, just not
			// inline-rendered.
			log.Printf("[telegram:%s] %q is %d bytes, over the %d photo limit - sending as document", t.botName, baseName, size, t.photoLimit())
		}
	}

	// Stream the file into the request body instead of buffering it, so a
	// large upload does not sit in memory in full.
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)

	go func() {
		// Any error here closes the pipe, which surfaces on the reader side as
		// the HTTP request fails.
		defer pipeWriter.Close()

		fields := map[string]string{"chat_id": chatID}
		if caption != "" {
			fields["caption"] = caption
		}
		if request.Thread != "" {
			if _, parseErr := strconv.ParseInt(request.Thread, 10, 64); parseErr == nil {
				fields["reply_to_message_id"] = request.Thread
			}
		}

		for key, value := range fields {
			if writeErr := writer.WriteField(key, value); writeErr != nil {
				pipeWriter.CloseWithError(writeErr)
				return
			}
		}

		part, partErr := writer.CreateFormFile(field, baseName)
		if partErr != nil {
			pipeWriter.CloseWithError(partErr)
			return
		}

		if _, copyErr := io.Copy(part, file); copyErr != nil {
			pipeWriter.CloseWithError(copyErr)
			return
		}

		if closeErr := writer.Close(); closeErr != nil {
			pipeWriter.CloseWithError(closeErr)
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/"+method, pipeReader)
	if err != nil {
		// Unblock the writer goroutine, which would otherwise sit on a pipe
		// write forever - the transport only closes the body once a request
		// actually runs.
		_ = pipeReader.CloseWithError(err)
		return protocol.Event{}, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.Event{}, fmt.Errorf("telegram %s failed: status %d", method, resp.StatusCode)
	}

	var sendResponse tgSendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResponse); err != nil {
		return protocol.Event{}, err
	}
	if !sendResponse.OK {
		return protocol.Event{}, fmt.Errorf("telegram %s returned not ok", method)
	}

	channel := strconv.FormatInt(sendResponse.Result.Chat.ID, 10)
	if channel == "0" {
		channel = chatID
	}

	thread := request.Thread
	if thread == "" && sendResponse.Result.MessageThreadID > 0 {
		thread = strconv.FormatInt(sendResponse.Result.MessageThreadID, 10)
	}

	target := request.Target
	if target == "" {
		target = "chat:" + channel
	}

	timestamp := time.Now().UTC()
	if sendResponse.Result.Date > 0 {
		timestamp = time.Unix(sendResponse.Result.Date, 0).UTC()
	}

	return protocol.Event{
		Timestamp:   timestamp,
		Service:     t.serviceName,
		Bot:         t.botName,
		Kind:        "message",
		Direction:   "out",
		User:        t.Identity(),
		Target:      target,
		Channel:     channel,
		Thread:      thread,
		Text:        caption,
		Attachments: []protocol.Attachment{t.describeSentFile(filePath, baseName, info.Size())},
	}, nil
}

// describeSentFile records what was uploaded. The sent bytes are copied into
// the media store so history stays truthful after the user moves or deletes
// their local copy - content addressing makes resending the same file free.
// If ingestion fails or storage is disabled, the attachment degrades to
// metadata rather than failing a send that already succeeded upstream.
func (t *TelegramConnector) describeSentFile(filePath string, baseName string, size int64) protocol.Attachment {
	attachment := protocol.Attachment{
		Name: media.SanitizeName(baseName),
		Size: size,
	}

	if !t.attachments.Enabled() {
		return attachment
	}

	source, err := os.Open(filePath)
	if err != nil {
		log.Printf("[telegram:%s] sent attachment %q not archived: %v", t.botName, baseName, err)
		return attachment
	}
	defer source.Close()

	ref, err := t.attachments.Put(baseName, "", source)
	if err != nil {
		log.Printf("[telegram:%s] sent attachment %q not archived: %v", t.botName, baseName, err)
		return attachment
	}

	attachment.Key = ref.Key
	attachment.Digest = ref.Digest
	attachment.Path = ref.Path
	attachment.Size = ref.Size
	if attachment.Name == "" {
		attachment.Name = ref.Name
	}

	return attachment
}

// telegramTextLength measures text the way the Bot API does: in UTF-16 code
// units. Counting runes would under-count emoji and other astral characters,
// which each occupy two units - exactly the content LLM-written captions are
// full of.
func telegramTextLength(text string) int {
	return len(utf16.Encode([]rune(text)))
}

// isTelegramInlineImage reports whether a filename should be sent via
// sendPhoto. Telegram re-encodes and compresses photos, so this is limited to
// formats where inline rendering is the expected behavior - notably excluding
// GIF, which belongs to sendAnimation, and SVG, which Telegram cannot render.
func isTelegramInlineImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

// collectAttachments turns the file-bearing fields of a Telegram message into
// protocol attachments, downloading the bytes when media storage is enabled.
//
// Download failures are logged and degrade to metadata-only attachments rather
// than dropping the message: knowing a file arrived and being unable to fetch
// it is strictly more useful than showing nothing.
func (t *TelegramConnector) collectAttachments(ctx context.Context, message *tgMessage) []protocol.Attachment {
	candidates := telegramFileCandidates(message)
	if len(candidates) == 0 {
		return nil
	}

	attachments := make([]protocol.Attachment, 0, len(candidates))
	for _, candidate := range candidates {
		attachment := protocol.Attachment{
			Name:     media.SanitizeName(candidate.FileName),
			MIME:     strings.TrimSpace(candidate.MIMEType),
			Size:     candidate.FileSize,
			RemoteID: candidate.FileID,
		}

		if t.attachments.Enabled() {
			ref, err := t.downloadAttachment(ctx, candidate)
			if err != nil {
				log.Printf("[telegram:%s] attachment %s not stored: %v", t.botName, candidate.FileID, err)
			} else {
				attachment.Key = ref.Key
				attachment.Digest = ref.Digest
				attachment.Path = ref.Path
				attachment.Size = ref.Size
				if attachment.Name == "" {
					attachment.Name = ref.Name
				}
				if attachment.MIME == "" {
					attachment.MIME = ref.MIME
				}
			}
		}

		attachments = append(attachments, attachment)
	}

	return attachments
}

// downloadAttachment resolves a file_id to a download path via getFile, then
// streams the bytes into the media store.
func (t *TelegramConnector) downloadAttachment(ctx context.Context, candidate tgFileMeta) (media.Ref, error) {
	filePath, err := t.resolveFilePath(ctx, candidate.FileID)
	if err != nil {
		return media.Ref{}, err
	}

	// Telegram returns a server-relative path; it selects the object to fetch
	// and never touches local storage, which is content-addressed by digest.
	downloadURL := t.fileBaseURL + "/" + strings.TrimLeft(filePath, "/")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return media.Ref{}, err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return media.Ref{}, fmt.Errorf("download attachment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return media.Ref{}, fmt.Errorf("download attachment: status %d", resp.StatusCode)
	}

	name := candidate.FileName
	if strings.TrimSpace(name) == "" {
		// Voice notes and stickers carry no file_name; fall back to the name
		// Telegram used on its own storage so the extension is preserved.
		name = path.Base(filePath)
	}

	mimeType := strings.TrimSpace(candidate.MIMEType)
	if mimeType == "" {
		mimeType = strings.TrimSpace(resp.Header.Get("Content-Type"))
	}

	return t.attachments.Put(name, mimeType, resp.Body)
}

func (t *TelegramConnector) resolveFilePath(ctx context.Context, fileID string) (string, error) {
	endpoint := t.baseURL + "/getFile?file_id=" + url.QueryEscape(fileID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("telegram getFile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("telegram getFile failed: status %d", resp.StatusCode)
	}

	var fileResponse tgGetFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileResponse); err != nil {
		return "", fmt.Errorf("telegram getFile decode: %w", err)
	}

	if !fileResponse.OK || strings.TrimSpace(fileResponse.Result.FilePath) == "" {
		return "", fmt.Errorf("telegram getFile returned no file_path for %s", fileID)
	}

	return fileResponse.Result.FilePath, nil
}

// telegramFileCandidates normalizes the message's file variants into a single
// list. Photos arrive as multiple pre-scaled sizes of the same image, so only
// the largest is taken.
func telegramFileCandidates(message *tgMessage) []tgFileMeta {
	if message == nil {
		return nil
	}

	var candidates []tgFileMeta

	if largest := largestTelegramPhoto(message.Photo); largest != nil {
		candidates = append(candidates, tgFileMeta{
			FileID:   largest.FileID,
			FileSize: largest.FileSize,
			MIMEType: "image/jpeg",
		})
	}

	for _, file := range []*tgFileMeta{
		message.Document,
		message.Audio,
		message.Video,
		message.Voice,
		message.VideoNote,
		message.Animation,
		message.Sticker,
	} {
		if file != nil && strings.TrimSpace(file.FileID) != "" {
			candidates = append(candidates, *file)
		}
	}

	return candidates
}

func largestTelegramPhoto(sizes []tgPhotoSize) *tgPhotoSize {
	var largest *tgPhotoSize
	for i := range sizes {
		if strings.TrimSpace(sizes[i].FileID) == "" {
			continue
		}
		if largest == nil || sizes[i].FileSize > largest.FileSize {
			largest = &sizes[i]
		}
	}
	return largest
}

func (t *TelegramConnector) loadSelf(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/getMe", nil)
	if err != nil {
		return err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("getMe failed: status %d", resp.StatusCode)
	}

	var me tgGetMeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return err
	}
	if !me.OK {
		return fmt.Errorf("getMe returned not ok")
	}

	t.mu.Lock()
	t.selfBotID = me.Result.ID
	t.mu.Unlock()

	return nil
}

func (t *TelegramConnector) getUpdates(ctx context.Context) ([]tgUpdate, error) {
	offset := t.currentOffset()
	payload := tgGetUpdatesRequest{
		Offset:         offset,
		Timeout:        50,
		AllowedUpdates: []string{"message", "edited_message", "channel_post", "edited_channel_post"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("getUpdates failed: status %d", resp.StatusCode)
	}

	var updatesResponse tgGetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&updatesResponse); err != nil {
		return nil, err
	}
	if !updatesResponse.OK {
		return nil, fmt.Errorf("getUpdates returned not ok")
	}

	return updatesResponse.Result, nil
}

func (t *TelegramConnector) currentOffset() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nextUpdateID
}

func (t *TelegramConnector) advanceOffset(next int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if next > t.nextUpdateID {
		t.nextUpdateID = next
	}
}

func (t *TelegramConnector) rememberChannel(channel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channels[channel] = struct{}{}
}

func (t *TelegramConnector) acceptsChannel(channel string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.channels) == 0 {
		return true
	}

	_, ok := t.channels[channel]
	return ok
}

// SupportsAttachments marks Telegram as able to deliver Request.Attach files.
func (t *TelegramConnector) SupportsAttachments() bool { return true }

// tgSendChatActionRequest is the payload for the sendChatAction endpoint.
type tgSendChatActionRequest struct {
	ChatID          string `json:"chat_id"`
	Action          string `json:"action"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
}

// Typing sends one "typing" chat action pulse. Telegram shows the status for
// about five seconds and then lets it decay, so a lease that wants a
// continuous indicator must call this repeatedly - that cadence is owned by
// the daemon, not the connector.
func (t *TelegramConnector) Typing(ctx context.Context, request protocol.Request) error {
	chatID := resolveTelegramChat(request)
	if chatID == "" {
		return fmt.Errorf("telegram typing requires channel or target")
	}

	payload := tgSendChatActionRequest{ChatID: chatID, Action: "typing"}
	if request.Thread != "" {
		if threadID, parseErr := strconv.ParseInt(request.Thread, 10, 64); parseErr == nil {
			payload.MessageThreadID = threadID
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/sendChatAction", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("telegram sendChatAction: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendChatAction failed: status %d", resp.StatusCode)
	}

	return nil
}

func (t *TelegramConnector) Identity() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.selfBotID > 0 {
		return strconv.FormatInt(t.selfBotID, 10)
	}
	return ""
}

func (t *TelegramConnector) isSelfMessage(message *tgMessage) bool {
	if message == nil || message.From == nil {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.selfBotID > 0 && message.From.ID == t.selfBotID
}

func (t *TelegramConnector) publishStatus(text string) {
	t.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (t *TelegramConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

func selectTelegramMessage(update tgUpdate) *tgMessage {
	if update.Message != nil {
		return update.Message
	}
	if update.EditedMessage != nil {
		return update.EditedMessage
	}
	if update.ChannelPost != nil {
		return update.ChannelPost
	}
	if update.EditedChannelPost != nil {
		return update.EditedChannelPost
	}
	return nil
}

func resolveTelegramChat(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"chat:", "telegram:chat:", "channel:", "telegram:channel:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}

func prepareTelegramSegments(format string, text string) ([]telegramOutboundSegment, error) {
	normalizedFormat, err := formatting.NormalizeFormat(format)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	var chunks []string

	switch normalizedFormat {
	case formatting.FormatPlain:
		chunks = formatting.SplitText(trimmed, 3500)
	case formatting.FormatHTML:
		// Split HTML at block-element boundaries so tags are never torn apart.
		chunks = formatting.SplitHTML(trimmed, 3500)
	case formatting.FormatMarkdown:
		// Convert the entire document to HTML first so that multi-paragraph
		// constructs (fenced code blocks, lists with blank lines, etc.) are
		// kept intact before splitting.
		htmlText, convertErr := formatting.MarkdownToHTML(trimmed)
		if convertErr != nil {
			return nil, fmt.Errorf("convert markdown to telegram html: %w", convertErr)
		}
		chunks = formatting.SplitHTML(htmlText, 3500)
	}

	segments := make([]telegramOutboundSegment, 0, len(chunks))

	for _, chunk := range chunks {
		switch normalizedFormat {
		case formatting.FormatPlain:
			segments = append(segments, telegramOutboundSegment{Text: chunk})
		case formatting.FormatHTML, formatting.FormatMarkdown:
			segments = append(segments, telegramOutboundSegment{Text: chunk, ParseMode: "HTML"})
		}
	}

	return segments, nil
}

// resolveChannelNames resolves any friendly channel references (e.g.
// "@mychannel") to Telegram numeric chat IDs via the getChat API. Entries
// that already look like numeric chat IDs are left unchanged.
func (t *TelegramConnector) resolveChannelNames(ctx context.Context) {
	t.mu.RLock()
	var toResolve []string
	for ch := range t.channels {
		if !isTelegramChatID(ch) {
			toResolve = append(toResolve, ch)
		}
	}
	t.mu.RUnlock()

	if len(toResolve) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range toResolve {
		chatID, err := t.getChatID(ctx, name)
		if err != nil {
			log.Printf("[telegram:%s] could not resolve channel %q: %v – keeping as-is", t.botName, name, err)
			continue
		}
		delete(t.channels, name)
		resolved := strconv.FormatInt(chatID, 10)
		t.channels[resolved] = struct{}{}
		log.Printf("[telegram:%s] resolved channel %q → %s", t.botName, name, resolved)
	}
}

func (t *TelegramConnector) getChatID(ctx context.Context, chatRef string) (int64, error) {
	payload, err := json.Marshal(map[string]string{"chat_id": chatRef})
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/getChat", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			ID int64 `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if !result.OK {
		return 0, fmt.Errorf("getChat failed for %q", chatRef)
	}
	return result.Result.ID, nil
}

// isTelegramChatID returns true when s looks like a Telegram numeric chat ID
// (a possibly-negative integer).
func isTelegramChatID(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// React is not supported by the Telegram connector.
func (t *TelegramConnector) React(_ context.Context, _ protocol.Request) error {
	return fmt.Errorf("reactions are not supported by the telegram connector")
}
