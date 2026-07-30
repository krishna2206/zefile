package api

import (
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	defaultThumbSize = 384
	minThumbSize     = 32
	maxThumbSize     = 1024

	// maxThumbPixels caps the source a thumbnail will decode. Without it a
	// decompression bomb — a tiny file that expands to a hundred megapixels —
	// could make the server allocate gigabytes to shrink one image.
	maxThumbPixels = 40_000_000
)

// handleThumb returns a small, re-encoded JPEG preview of an image.
//
// The original bytes are decoded and re-encoded, so what leaves here is a raster
// this server produced — never the uploaded file — which is why it is safe to
// serve on the application origin rather than needing the content host. The
// point is bandwidth: a folder of four-megabyte photos should cost kilobytes to
// browse, not megabytes per tile.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(w, r)
	if !ok {
		return
	}

	size := defaultThumbSize
	if raw := r.URL.Query().Get("s"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			size = n
		}
	}
	size = min(max(size, minThumbSize), maxThumbSize)

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if info.IsDir {
		writeProblem(w, r, http.StatusUnsupportedMediaType, CodeBadRequest,
			"No thumbnail", "A folder has no thumbnail.")
		return
	}

	// A cheap identity from the metadata: the same bytes at the same size get the
	// same tag, so a revisit is a 304 and never re-decodes. no-cache keeps a
	// browser revalidating, so replacing the file shows the new thumbnail.
	etag := fmt.Sprintf(`"t%d-%d-%d"`, info.ModTime.UnixNano(), info.Size, size)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	file, err := s.fs.Open(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer file.Close()

	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		writeProblem(w, r, http.StatusUnsupportedMediaType, CodeBadRequest,
			"No thumbnail", "This file is not an image that can be previewed.")
		return
	}
	if cfg.Width*cfg.Height > maxThumbPixels {
		writeProblem(w, r, http.StatusUnsupportedMediaType, CodeTooLarge,
			"Too large", "This image is too large to make a thumbnail of.")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, r, err)
		return
	}

	src, _, err := image.Decode(file)
	if err != nil {
		writeProblem(w, r, http.StatusUnsupportedMediaType, CodeBadRequest,
			"No thumbnail", "This file is not an image that can be previewed.")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := jpeg.Encode(w, resizeToFit(src, size), &jpeg.Options{Quality: 78}); err != nil {
		// The status and headers are already on the wire, so there is nothing to
		// send but a log line; the client sees a truncated image and retries.
		slog.ErrorContext(r.Context(), "thumbnail encode failed", "error", err)
	}
}

// resizeToFit scales an image down to fit within a max square, keeping its
// aspect ratio. Images already smaller are returned untouched.
func resizeToFit(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return src
	}
	nw, nh := maxSide, maxSide
	if w >= h {
		nh = h * maxSide / w
	} else {
		nw = w * maxSide / h
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(nw, 1), max(nh, 1)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
