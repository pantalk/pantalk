// Package media stores attachment bytes for inbound and outbound messages.
//
// Attachments are content-addressed by SHA-256: the digest is the identity of
// a file, so the same image forwarded through three chats is stored once, and
// nothing an upstream platform sends can influence the path we write to. The
// original filename travels as metadata only.
//
// Store is an interface rather than a concrete type because the filesystem is
// only the first backend. Object storage can be added later without changing
// the config surface or any connector code.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrTooLarge is returned when an attachment exceeds the configured cap. It is
// deliberately distinguishable so connectors can log and skip a single
// oversized file rather than tearing down the session.
var ErrTooLarge = errors.New("attachment exceeds configured size limit")

// Ref describes a stored attachment.
//
// Key is the handle the backend uses to find these bytes again, and is the
// only locator worth persisting - it stays valid when the storage root moves,
// and it means something to whichever backend is configured. The filesystem
// backend uses the content digest as its key; another backend is free to use
// an object key or any other opaque token.
//
// Path is a convenience for the current process only. It is deliberately not
// something callers should record: it bakes in both the backend and the root
// directory that happened to be configured when the bytes were written.
type Ref struct {
	Key    string
	Digest string
	Size   int64
	Name   string
	MIME   string
	Path   string
}

// Store persists attachment bytes and resolves them back by key.
type Store interface {
	// Put streams r into storage, returning a Ref describing what was
	// written. Implementations must enforce their own size limit and must
	// not trust name for path construction.
	Put(name string, mime string, r io.Reader) (Ref, error)

	// Open returns a reader for a previously stored key.
	Open(key string) (io.ReadCloser, error)

	// LocalPath resolves a key to a filesystem path when the backend can
	// expose one without copying. The second result reports whether a path
	// is available; backends that hold bytes remotely return false.
	LocalPath(key string) (string, bool)

	// Enabled reports whether bytes are actually persisted. When false,
	// callers should record attachment metadata but skip downloads.
	Enabled() bool
}

// CollectResult summarizes what a garbage collection pass reclaimed.
type CollectResult struct {
	Deleted  int
	Bytes    int64
	Scanned  int
	Retained int
}

// Collector is implemented by backends that can reclaim bytes no longer
// referenced by stored history. It is deliberately separate from Store: a
// remote backend may prefer to express retention through its own lifecycle
// rules rather than a sweep driven from here.
type Collector interface {
	// Collect deletes stored objects whose key is absent from referenced.
	// Objects newer than notAfter are always kept, which is what makes the
	// sweep safe to run while messages are arriving.
	Collect(referenced map[string]struct{}, notAfter time.Time) (CollectResult, error)
}

// FSStore keeps attachments on the local filesystem, sharded by the first byte
// of the digest so a single directory never accumulates unbounded entries.
type FSStore struct {
	root     string
	maxBytes int64
}

// NewFSStore creates the storage root and returns a filesystem-backed Store.
func NewFSStore(root string, maxBytes int64) (*FSStore, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, errors.New("media root cannot be empty")
	}

	if maxBytes <= 0 {
		return nil, errors.New("media max_bytes must be positive")
	}

	if err := os.MkdirAll(trimmed, 0o700); err != nil {
		return nil, fmt.Errorf("create media root %q: %w", trimmed, err)
	}

	return &FSStore{root: trimmed, maxBytes: maxBytes}, nil
}

func (s *FSStore) Enabled() bool { return true }

// Root returns the storage root directory.
func (s *FSStore) Root() string { return s.root }

// Put streams r to a temporary file while hashing it, then renames the result
// into its content-addressed home. Writing to a temp file first means a failed
// or oversized download never leaves a partial file behind that a later reader
// would mistake for complete.
func (s *FSStore) Put(name string, mime string, r io.Reader) (Ref, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return Ref{}, fmt.Errorf("create media root: %w", err)
	}

	temp, err := os.CreateTemp(s.root, ".partial-*")
	if err != nil {
		return Ref{}, fmt.Errorf("create temp file: %w", err)
	}
	tempName := temp.Name()

	// Clean up the temp file on every path that does not rename it away.
	committed := false
	defer func() {
		temp.Close()
		if !committed {
			os.Remove(tempName)
		}
	}()

	hasher := sha256.New()

	// Read one byte past the limit so hitting the cap exactly is allowed but
	// exceeding it is detected without buffering the whole file.
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return Ref{}, fmt.Errorf("write attachment: %w", err)
	}
	if written > s.maxBytes {
		return Ref{}, fmt.Errorf("%w (%d bytes, limit %d)", ErrTooLarge, written, s.maxBytes)
	}

	if err := temp.Close(); err != nil {
		return Ref{}, fmt.Errorf("close temp file: %w", err)
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	finalPath := s.pathFor(digest, name)

	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return Ref{}, fmt.Errorf("create media shard: %w", err)
	}

	// An existing file means we already hold these exact bytes, so the temp
	// copy is redundant. Leaving `committed` false lets the deferred cleanup
	// remove it - marking it committed here would strand a .partial file in
	// the media root for every duplicate attachment.
	//
	// The mtime refresh is load-bearing: Collect's grace period treats "not
	// recently modified" as "safe to sweep if unreferenced". A dedup hit on an
	// old file re-enters the bytes-before-row window, so without the refresh a
	// concurrent sweep could delete the file between this Put returning and
	// the referencing row being written.
	if _, statErr := os.Stat(finalPath); statErr == nil {
		now := time.Now()
		if err := os.Chtimes(finalPath, now, now); err != nil {
			return Ref{}, fmt.Errorf("refresh attachment mtime: %w", err)
		}
	} else {
		if err := os.Rename(tempName, finalPath); err != nil {
			return Ref{}, fmt.Errorf("commit attachment: %w", err)
		}
		committed = true

		if err := os.Chmod(finalPath, 0o600); err != nil {
			return Ref{}, fmt.Errorf("chmod attachment: %w", err)
		}
	}

	return Ref{
		// The filesystem backend is content-addressed, so the digest is also
		// its lookup key.
		Key:    digest,
		Digest: digest,
		Size:   written,
		Name:   SanitizeName(name),
		MIME:   strings.TrimSpace(mime),
		Path:   finalPath,
	}, nil
}

// Open returns a reader for a stored key.
func (s *FSStore) Open(key string) (io.ReadCloser, error) {
	path, err := s.Locate(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// LocalPath resolves a key to its on-disk location.
func (s *FSStore) LocalPath(key string) (string, bool) {
	path, err := s.Locate(key)
	if err != nil {
		return "", false
	}
	return path, true
}

// Locate resolves a key to its on-disk path, regardless of the extension the
// bytes were stored with.
func (s *FSStore) Locate(key string) (string, error) {
	clean, err := validateKey(key)
	if err != nil {
		return "", err
	}

	shard := filepath.Join(s.root, clean[:2])
	entries, err := os.ReadDir(shard)
	if err != nil {
		return "", fmt.Errorf("attachment %s not found: %w", clean, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base := entry.Name()
		if base == clean || strings.HasPrefix(base, clean+".") {
			return filepath.Join(shard, base), nil
		}
	}

	return "", fmt.Errorf("attachment %s not found", clean)
}

// pathFor builds the content-addressed destination. Only the extension is
// carried over from the upstream name, and only after sanitizing - the digest
// supplies the filename, so a hostile name cannot escape the root.
func (s *FSStore) pathFor(digest string, name string) string {
	base := digest
	if ext := safeExtension(name); ext != "" {
		base += ext
	}
	return filepath.Join(s.root, digest[:2], base)
}

// Collect removes attachments that no stored message references any more.
//
// Two rules keep this safe to run against a live daemon:
//
//   - Only files this store recognizes are considered. A name must parse as a
//     content key (optionally with an extension) inside a two-character shard
//     directory. Anything else a user has put in the media root is left alone.
//   - Only files last modified at or before notAfter are eligible. Bytes are
//     written before the row that references them exists, so without a grace
//     period a sweep could delete an attachment mid-delivery.
func (s *FSStore) Collect(referenced map[string]struct{}, notAfter time.Time) (CollectResult, error) {
	var result CollectResult

	shards, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read media root: %w", err)
	}

	for _, shard := range shards {
		if !shard.IsDir() {
			// Abandoned temp files from a crashed or killed write.
			if strings.HasPrefix(shard.Name(), ".partial-") {
				s.collectStrayTemp(filepath.Join(s.root, shard.Name()), notAfter, &result)
			}
			continue
		}

		if !isShardName(shard.Name()) {
			continue
		}

		shardPath := filepath.Join(s.root, shard.Name())
		entries, readErr := os.ReadDir(shardPath)
		if readErr != nil {
			return result, fmt.Errorf("read media shard %q: %w", shard.Name(), readErr)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			key, ok := keyFromStoredName(entry.Name())
			if !ok || !strings.HasPrefix(key, shard.Name()) {
				// Not something this store wrote; leave it untouched.
				continue
			}

			result.Scanned++

			if _, stillReferenced := referenced[key]; stillReferenced {
				result.Retained++
				continue
			}

			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			if info.ModTime().After(notAfter) {
				// Written too recently to be sure its row exists yet.
				result.Retained++
				continue
			}

			if removeErr := os.Remove(filepath.Join(shardPath, entry.Name())); removeErr != nil {
				if os.IsNotExist(removeErr) {
					continue
				}
				return result, fmt.Errorf("remove orphaned attachment: %w", removeErr)
			}

			result.Deleted++
			result.Bytes += info.Size()
		}

		// Drop the shard directory once it is empty, so the root does not
		// accumulate 256 empty folders forever.
		if remaining, readErr := os.ReadDir(shardPath); readErr == nil && len(remaining) == 0 {
			_ = os.Remove(shardPath)
		}
	}

	return result, nil
}

func (s *FSStore) collectStrayTemp(path string, notAfter time.Time, result *CollectResult) {
	info, err := os.Stat(path)
	if err != nil || info.ModTime().After(notAfter) {
		return
	}

	if err := os.Remove(path); err == nil {
		result.Deleted++
		result.Bytes += info.Size()
	}
}

// isShardName reports whether a directory name is one of the two-hex-character
// shards this store creates.
func isShardName(name string) bool {
	if len(name) != 2 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}

// keyFromStoredName recovers the content key from a stored filename, which is
// the key optionally followed by a single extension.
func keyFromStoredName(name string) (string, bool) {
	base := name
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}

	key, err := validateKey(base)
	if err != nil {
		return "", false
	}

	return key, true
}

// NoopStore records metadata without persisting bytes. It backs the "none"
// media backend.
type NoopStore struct{}

func (NoopStore) Enabled() bool { return false }

func (NoopStore) Put(string, string, io.Reader) (Ref, error) {
	return Ref{}, errors.New("media storage is disabled (server.media.backend: none)")
}

func (NoopStore) Open(string) (io.ReadCloser, error) {
	return nil, errors.New("media storage is disabled (server.media.backend: none)")
}

func (NoopStore) LocalPath(string) (string, bool) { return "", false }

// SanitizeName reduces an upstream-supplied filename to a bare, printable base
// name. The result is metadata only and is never used to build a path, but it
// is still cleaned so it is safe to log, display, and round-trip through JSON.
func SanitizeName(name string) string {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return ""
	}

	// Strip any directory structure, including Windows-style separators that
	// filepath.Base leaves alone on Unix.
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	cleaned = path.Base(cleaned)

	// Drop control characters and leading dots so the name cannot masquerade
	// as a hidden or relative path.
	cleaned = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, cleaned)
	cleaned = strings.TrimLeft(cleaned, ".")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" || cleaned == "/" {
		return ""
	}

	const maxNameLen = 128
	if len(cleaned) > maxNameLen {
		cleaned = cleaned[:maxNameLen]
	}

	return cleaned
}

// safeExtension returns a conservative lowercase extension for an upstream
// filename, or "" when there is nothing trustworthy to use.
func safeExtension(name string) string {
	ext := strings.ToLower(path.Ext(SanitizeName(name)))
	if len(ext) < 2 || len(ext) > 12 {
		return ""
	}

	for _, r := range ext[1:] {
		isAlphanumeric := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !isAlphanumeric {
			return ""
		}
	}

	return ext
}

// validateKey checks that a key is well formed for this backend. Filesystem
// keys are content digests, so anything that is not a bare hex SHA-256 is
// rejected before it can reach the filesystem.
func validateKey(key string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(key))
	if len(clean) != sha256.Size*2 {
		return "", fmt.Errorf("invalid attachment key %q", key)
	}

	if _, err := hex.DecodeString(clean); err != nil {
		return "", fmt.Errorf("invalid attachment key %q", key)
	}

	return clean, nil
}
