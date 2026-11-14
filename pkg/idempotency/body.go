package idempotency

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
)

// Chunks grow from firstChunk to maxChunk as the body arrives, so a small JSON
// body costs a few KiB and a 10 MiB upload about 10 MiB, never a preallocation
// sized by a client-announced Content-Length.
const (
	firstChunk = 4 << 10
	maxChunk   = 256 << 10
)

// hashRequestBody reads r's body once, up to limit bytes, and returns the request
// hash — SHA-256 over target, a zero byte and the body, the same value NewClaim
// computes from a []byte, so keys stored by either path compare equal. The body
// is hashed while it is read, kept in ONE set of chunks, and r.Body is replaced
// with a reader over those chunks for the handler. A body over limit is 413.
//
// With multipart set and a multipart/form-data content type, the hash is the
// canonical one instead (see WithMultipartHash).
func hashRequestBody(r *http.Request, target string, limit int64, multipartHash bool) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	if r.Body == nil || r.Body == http.NoBody {
		return rawHash(target, nil), nil
	}
	if r.ContentLength > limit {
		return sum, tooLarge()
	}
	h := sha256.New()
	h.Write([]byte(target))
	h.Write([]byte{0})
	chunks, err := spool(r.Body, h, limit, r.ContentLength)
	if err != nil {
		return sum, err
	}
	r.Body = io.NopCloser(chunkReader(chunks))
	if multipartHash {
		if canon, ok := canonicalMultipart(r.Header.Get("Content-Type"), target, chunks); ok {
			return canon, nil
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func rawHash(target string, body []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(target))
	h.Write([]byte{0})
	h.Write(body)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// spool reads src into chunks, feeding h as it goes, and refuses more than limit
// bytes. It reads limit+1 at most, so "over the limit" is detected without
// consuming the rest of an enormous body.
func spool(src io.Reader, h hash.Hash, limit, hint int64) (chunks [][]byte, err error) {
	var total int64
	lr := &io.LimitedReader{R: src, N: limit + 1}
	size := firstChunk
	if hint > 0 && hint < int64(maxChunk) {
		size = int(hint) + 1 // the whole body and the EOF in one read
	}
	for {
		buf := make([]byte, size)
		n, rerr := io.ReadFull(lr, buf)
		if n > 0 {
			h.Write(buf[:n])
			chunks = append(chunks, buf[:n])
			total += int64(n)
		}
		if rerr == io.EOF || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return nil, commonerrors.New(http.StatusBadRequest).
				Reason(ReasonBodyUnreadable).Message("the request body could not be read").Cause(rerr).Build()
		}
		size = min(size*2, maxChunk)
	}
	if total > limit {
		return nil, tooLarge()
	}
	return chunks, nil
}

func tooLarge() error {
	return commonerrors.New(http.StatusRequestEntityTooLarge).
		Reason(ReasonBodyTooLarge).Message("the request body is too large").Build()
}

func chunkReader(chunks [][]byte) io.Reader {
	readers := make([]io.Reader, len(chunks))
	for i, c := range chunks {
		readers[i] = bytes.NewReader(c)
	}
	return io.MultiReader(readers...)
}

// canonicalMultipart hashes a multipart/form-data body without its boundary: for
// each part, in order, the field name, file name, content type and the SHA-256 of
// its content, each length-prefixed so that moving a byte from one field into the
// next cannot give the same hash. ok is false — fall back to the raw hash — when
// the content type is not multipart/form-data or the body does not parse.
func canonicalMultipart(contentType, target string, chunks [][]byte) (sum [sha256.Size]byte, ok bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return sum, false
	}
	h := sha256.New()
	writeField(h, []byte("multipart/v1"))
	writeField(h, []byte(target))
	mr := multipart.NewReader(chunkReader(chunks), params["boundary"])
	parts := 0
	for {
		part, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sum, false
		}
		parts++
		content := sha256.New()
		if _, err := io.Copy(content, part); err != nil {
			return sum, false
		}
		_, disp, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		writeField(h, []byte(disp["name"]))
		writeField(h, []byte(disp["filename"]))
		writeField(h, []byte(part.Header.Get("Content-Type")))
		writeField(h, content.Sum(nil))
	}
	if parts == 0 {
		return sum, false // no terminating boundary or nothing in it: not a form we understand
	}
	copy(sum[:], h.Sum(nil))
	return sum, true
}

// writeField writes b length-prefixed.
func writeField(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}
