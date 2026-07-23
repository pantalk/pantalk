package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T, maxBytes int64) *FSStore {
	t.Helper()

	store, err := NewFSStore(t.TempDir(), maxBytes)
	if err != nil {
		t.Fatalf("new fs store: %v", err)
	}

	return store
}

func TestPutStoresContentAddressed(t *testing.T) {
	store := newTestStore(t, 1024)

	payload := "hello attachment"
	ref, err := store.Put("photo.JPG", "image/jpeg", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	sum := sha256.Sum256([]byte(payload))
	wantDigest := hex.EncodeToString(sum[:])

	if ref.Digest != wantDigest {
		t.Fatalf("digest = %q, want %q", ref.Digest, wantDigest)
	}
	if ref.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", ref.Size, len(payload))
	}

	// The extension is normalized to lowercase and the digest supplies the
	// filename, sharded by the first two hex characters.
	wantPath := filepath.Join(store.Root(), wantDigest[:2], wantDigest+".jpg")
	if ref.Path != wantPath {
		t.Fatalf("path = %q, want %q", ref.Path, wantPath)
	}

	stored, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(stored) != payload {
		t.Fatalf("stored content = %q, want %q", stored, payload)
	}
}

func TestPutDeduplicatesIdenticalContent(t *testing.T) {
	store := newTestStore(t, 1024)

	first, err := store.Put("a.txt", "text/plain", strings.NewReader("same bytes"))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	second, err := store.Put("b.txt", "text/plain", strings.NewReader("same bytes"))
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first.Path != second.Path {
		t.Fatalf("identical content stored twice: %q vs %q", first.Path, second.Path)
	}

	// No temp files should survive a successful write.
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") {
			t.Fatalf("leftover temp file %q", entry.Name())
		}
	}
}

func TestPutRejectsOversizedContentWithoutLeavingPartials(t *testing.T) {
	store := newTestStore(t, 8)

	_, err := store.Put("big.bin", "application/octet-stream", strings.NewReader("far too many bytes"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized put left %d entries behind", len(entries))
	}
}

func TestPutAcceptsContentExactlyAtLimit(t *testing.T) {
	store := newTestStore(t, 4)

	ref, err := store.Put("exact.bin", "application/octet-stream", strings.NewReader("abcd"))
	if err != nil {
		t.Fatalf("put at exact limit: %v", err)
	}
	if ref.Size != 4 {
		t.Fatalf("size = %d, want 4", ref.Size)
	}
}

// A hostile upstream filename must not influence where bytes land. The digest
// is the only thing that determines the path.
func TestPutIgnoresTraversalInName(t *testing.T) {
	store := newTestStore(t, 1024)

	for _, name := range []string{
		"../../../etc/passwd",
		"..\\..\\windows\\system32\\cfg",
		"/etc/shadow",
		"....//....//escape.txt",
	} {
		ref, err := store.Put(name, "text/plain", strings.NewReader("payload-"+name))
		if err != nil {
			t.Fatalf("put %q: %v", name, err)
		}

		absoluteRoot, err := filepath.Abs(store.Root())
		if err != nil {
			t.Fatalf("abs root: %v", err)
		}
		absoluteStored, err := filepath.Abs(ref.Path)
		if err != nil {
			t.Fatalf("abs stored: %v", err)
		}

		if !strings.HasPrefix(absoluteStored, absoluteRoot+string(os.PathSeparator)) {
			t.Fatalf("name %q escaped the media root: %q", name, absoluteStored)
		}
		if strings.Contains(ref.Name, "/") || strings.Contains(ref.Name, "\\") {
			t.Fatalf("sanitized name %q still contains a separator", ref.Name)
		}
	}
}

func TestOpenAndLocateRoundTrip(t *testing.T) {
	store := newTestStore(t, 1024)

	ref, err := store.Put("note.txt", "text/plain", strings.NewReader("round trip"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	reader, err := store.Open(ref.Digest)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "round trip" {
		t.Fatalf("content = %q, want %q", content, "round trip")
	}
}

func TestOpenRejectsMalformedDigest(t *testing.T) {
	store := newTestStore(t, 1024)

	for _, digest := range []string{
		"",
		"short",
		"../../../etc/passwd",
		strings.Repeat("z", 64), // right length, not hex
	} {
		if _, err := store.Open(digest); err == nil {
			t.Fatalf("digest %q was accepted", digest)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"photo.jpg", "photo.jpg"},
		{"../../etc/passwd", "passwd"},
		{"C:\\temp\\report.pdf", "report.pdf"},
		{"  spaced.txt  ", "spaced.txt"},
		{".hidden", "hidden"},
		{"", ""},
		{"/", ""},
		{"with\x00null.txt", "withnull.txt"},
		{"tab\tname.txt", "tabname.txt"},
	}

	for _, test := range tests {
		if got := SanitizeName(test.in); got != test.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestSanitizeNameTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("a", 500) + ".txt"

	got := SanitizeName(long)
	if len(got) > 128 {
		t.Fatalf("name length = %d, want <= 128", len(got))
	}
}

func TestSafeExtension(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"photo.JPG", ".jpg"},
		{"archive.tar.gz", ".gz"},
		{"noext", ""},
		{"trailing.", ""},
		{"weird.na me", ""},
		{"long." + strings.Repeat("x", 20), ""},
		{"dashed.ta-r", ""},
	}

	for _, test := range tests {
		if got := safeExtension(test.in); got != test.want {
			t.Errorf("safeExtension(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestNoopStoreReportsDisabled(t *testing.T) {
	var store Store = NoopStore{}

	if store.Enabled() {
		t.Fatal("NoopStore reported enabled")
	}
	if _, err := store.Put("a.txt", "text/plain", strings.NewReader("x")); err == nil {
		t.Fatal("NoopStore.Put succeeded, want error")
	}
	if _, err := store.Open(strings.Repeat("a", 64)); err == nil {
		t.Fatal("NoopStore.Open succeeded, want error")
	}
}

func TestNewFSStoreRejectsInvalidConfig(t *testing.T) {
	if _, err := NewFSStore("", 1024); err == nil {
		t.Fatal("empty root was accepted")
	}
	if _, err := NewFSStore(t.TempDir(), 0); err == nil {
		t.Fatal("zero max bytes was accepted")
	}
}

// past returns a timestamp comfortably beyond any grace period, so freshly
// written test files are eligible for collection.
func past() time.Time { return time.Now().Add(time.Hour) }

func TestCollectRemovesUnreferencedFiles(t *testing.T) {
	store := newTestStore(t, 1024)

	keep, err := store.Put("keep.txt", "text/plain", strings.NewReader("keep me"))
	if err != nil {
		t.Fatalf("put keep: %v", err)
	}
	drop, err := store.Put("drop.txt", "text/plain", strings.NewReader("drop me"))
	if err != nil {
		t.Fatalf("put drop: %v", err)
	}

	referenced := map[string]struct{}{keep.Key: {}}

	result, err := store.Collect(referenced, past())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if result.Retained != 1 {
		t.Fatalf("retained = %d, want 1", result.Retained)
	}
	if result.Bytes != int64(len("drop me")) {
		t.Fatalf("bytes = %d, want %d", result.Bytes, len("drop me"))
	}

	if _, err := os.Stat(keep.Path); err != nil {
		t.Fatalf("referenced file was removed: %v", err)
	}
	if _, err := os.Stat(drop.Path); !os.IsNotExist(err) {
		t.Fatalf("orphan still present: %v", err)
	}
}

// Content addressing means several messages can share one file. It must
// survive until the last reference is gone.
func TestCollectKeepsSharedKeyUntilLastReferenceGoes(t *testing.T) {
	store := newTestStore(t, 1024)

	first, err := store.Put("a.txt", "text/plain", strings.NewReader("shared bytes"))
	if err != nil {
		t.Fatalf("put a: %v", err)
	}
	second, err := store.Put("b.txt", "text/plain", strings.NewReader("shared bytes"))
	if err != nil {
		t.Fatalf("put b: %v", err)
	}
	if first.Key != second.Key {
		t.Fatalf("expected dedupe to the same key, got %q and %q", first.Key, second.Key)
	}

	// One of two referencing rows removed: the file must stay.
	result, err := store.Collect(map[string]struct{}{first.Key: {}}, past())
	if err != nil {
		t.Fatalf("collect with reference: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("deleted a still-referenced file (%d)", result.Deleted)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("shared file removed too early: %v", err)
	}

	// Last reference gone: now it may go.
	result, err = store.Collect(map[string]struct{}{}, past())
	if err != nil {
		t.Fatalf("collect without references: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
}

// Bytes are written before the row referencing them exists, so a sweep must
// not touch anything inside the grace period.
func TestCollectRespectsGracePeriod(t *testing.T) {
	store := newTestStore(t, 1024)

	ref, err := store.Put("fresh.txt", "text/plain", strings.NewReader("just arrived"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Nothing references it, but it was written moments ago.
	result, err := store.Collect(map[string]struct{}{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if result.Deleted != 0 {
		t.Fatalf("deleted a file inside the grace period (%d)", result.Deleted)
	}
	if _, err := os.Stat(ref.Path); err != nil {
		t.Fatalf("in-flight attachment was removed: %v", err)
	}
}

// Files the store did not write are never touched, however old they are.
func TestCollectIgnoresForeignFiles(t *testing.T) {
	store := newTestStore(t, 1024)

	// A user's own file dropped in the root.
	rootFile := filepath.Join(store.Root(), "my-notes.txt")
	if err := os.WriteFile(rootFile, []byte("mine"), 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	// A non-shard directory.
	otherDir := filepath.Join(store.Root(), "backups")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := filepath.Join(otherDir, "old.txt")
	if err := os.WriteFile(nested, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	// A non-key filename inside a real shard directory.
	shard := filepath.Join(store.Root(), "ab")
	if err := os.MkdirAll(shard, 0o700); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	strayInShard := filepath.Join(shard, "not-a-digest.txt")
	if err := os.WriteFile(strayInShard, []byte("stray"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	result, err := store.Collect(map[string]struct{}{}, past())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("collected foreign files (%d)", result.Deleted)
	}

	for _, p := range []string{rootFile, nested, strayInShard} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("foreign file %q was removed: %v", p, err)
		}
	}
}

// A digest-named file must live in the shard matching its own prefix; one that
// does not is not something this store wrote.
func TestCollectIgnoresKeyInWrongShard(t *testing.T) {
	store := newTestStore(t, 1024)

	key := strings.Repeat("ab", 32)
	shard := filepath.Join(store.Root(), "cd")
	if err := os.MkdirAll(shard, 0o700); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}

	misplaced := filepath.Join(shard, key+".txt")
	if err := os.WriteFile(misplaced, []byte("misplaced"), 0o600); err != nil {
		t.Fatalf("write misplaced: %v", err)
	}

	result, err := store.Collect(map[string]struct{}{}, past())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("collected a file from a mismatched shard (%d)", result.Deleted)
	}
	if _, err := os.Stat(misplaced); err != nil {
		t.Fatalf("misplaced file removed: %v", err)
	}
}

func TestCollectRemovesStaleTempFiles(t *testing.T) {
	store := newTestStore(t, 1024)

	stale := filepath.Join(store.Root(), ".partial-123456")
	if err := os.WriteFile(stale, []byte("crashed mid-write"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	result, err := store.Collect(map[string]struct{}{}, past())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp file survived: %v", err)
	}
}

func TestCollectLeavesRecentTempFiles(t *testing.T) {
	store := newTestStore(t, 1024)

	fresh := filepath.Join(store.Root(), ".partial-999")
	if err := os.WriteFile(fresh, []byte("in flight"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	result, err := store.Collect(map[string]struct{}{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("collected an in-flight temp file (%d)", result.Deleted)
	}
}

func TestCollectRemovesEmptyShardDirectories(t *testing.T) {
	store := newTestStore(t, 1024)

	ref, err := store.Put("gone.txt", "text/plain", strings.NewReader("delete me"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, err := store.Collect(map[string]struct{}{}, past()); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(ref.Path)); !os.IsNotExist(err) {
		t.Fatalf("empty shard directory survived: %v", err)
	}
}

func TestCollectOnEmptyStore(t *testing.T) {
	store := newTestStore(t, 1024)

	result, err := store.Collect(map[string]struct{}{}, past())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 0 || result.Scanned != 0 {
		t.Fatalf("unexpected result on empty store: %+v", result)
	}
}

func TestFSStoreSatisfiesCollector(t *testing.T) {
	var _ Collector = (*FSStore)(nil)
}

// A dedup hit re-enters the bytes-before-row window, so it must refresh the
// file's mtime - otherwise a concurrent sweep could delete an old file between
// Put returning and the new referencing row being written.
func TestPutDedupeRefreshesGracePeriod(t *testing.T) {
	store := newTestStore(t, 1024)

	ref, err := store.Put("a.txt", "text/plain", strings.NewReader("recurring bytes"))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	// Age the file well past any grace period.
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(ref.Path, stale, stale); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// The same bytes arrive again (dedupe path).
	if _, err := store.Put("b.txt", "text/plain", strings.NewReader("recurring bytes")); err != nil {
		t.Fatalf("second put: %v", err)
	}

	// A sweep with a one-hour grace period and no references must now keep
	// the file, because the dedup hit renewed its window.
	result, err := store.Collect(map[string]struct{}{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("freshly deduped file was swept (%d deleted)", result.Deleted)
	}
	if _, err := os.Stat(ref.Path); err != nil {
		t.Fatalf("file missing after sweep: %v", err)
	}
}
