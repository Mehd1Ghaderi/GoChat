package web

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/core/audio"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

const (
	chunkSize         = 64 << 10
	maxFilesPerUpload = 10
)

// upload streams each file part of a multipart request to the server
// without buffering it, and answers with the chips the composer posts. With
// ?voice=1 the one file is a voice message, ?duration its length in ms.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	q := r.URL.Query()
	chID, form := q.Get("channel"), q.Get("form")
	voice := q.Get("voice") == "1"
	var duration uint32
	if d, err := strconv.ParseUint(q.Get("duration"), 10, 32); err == nil {
		duration = uint32(d)
	}
	if form == "" {
		form = "composer"
	}
	if !uuidRe.MatchString(chID) || (form != "composer" && form != "reply-composer") {
		h.notFound(w, r)
		return
	}
	rc := http.NewResponseController(w)
	end := time.Now().Add(h.cfg.HTTP.TransferTimeout)
	_ = rc.SetWriteDeadline(end)
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.UploadLimit*maxFilesPerUpload+1<<20)
	r.Body = &idleBody{ReadCloser: r.Body, rc: rc, idle: h.cfg.HTTP.TransferIdleTimeout, end: end}
	mr, err := r.MultipartReader()
	if err != nil {
		h.fail(w, r, ierrors.InvalidArgument("expected a multipart upload"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.HTTP.TransferTimeout)
	defer cancel()
	ctx = h.client.Stream(ctx, h.call(r))

	var atts []*pb.Attachment
	buf := make([]byte, chunkSize)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.fail(w, r, ierrors.InvalidArgument("the upload was cut short"))
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_ = part.Close()
			continue
		}
		if len(atts) == maxFilesPerUpload || (voice && len(atts) == 1) {
			h.fail(w, r, ierrors.InvalidArgument("attach at most 10 files at once, or one voice message"))
			return
		}
		header := &pb.UploadHeader{ChannelId: chID, Name: part.FileName(), Voice: voice}
		if voice {
			header.DurationMs = duration
		}
		att, err := h.sendFile(ctx, header, part, buf)
		_ = part.Close()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		atts = append(atts, att)
	}
	var out strings.Builder
	for _, att := range atts {
		if err := h.render.Fragment(&out, "chip", struct {
			*pb.Attachment
			Form string
		}{att, form}); err != nil {
			h.fail(w, r, ierrors.Internal(err))
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, out.String())
}

func (h *Handler) sendFile(ctx context.Context, header *pb.UploadHeader, body io.Reader, buf []byte) (*pb.Attachment, error) {
	stream, err := h.client.Files.Upload(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Send(&pb.UploadRequest{Part: &pb.UploadRequest_Header{Header: header}}); err != nil {
		return nil, err
	}
	var sent int64
	for {
		n, err := body.Read(buf)
		if n > 0 {
			sent += int64(n)
			if sent > h.cfg.UploadLimit {
				return nil, ierrors.InvalidArgument("file is too large")
			}
			if serr := stream.Send(&pb.UploadRequest{Part: &pb.UploadRequest_Chunk{Chunk: buf[:n]}}); serr != nil {
				return nil, h.streamErr(stream, serr)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ierrors.InvalidArgument("the upload was cut short")
		}
	}
	res, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}
	return res.Attachment, nil
}

// idleBody fails an upload that stalls, so a slow sender cannot hold an
// upload slot for the whole transfer timeout.
type idleBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
	end  time.Time
}

func (b *idleBody) Read(p []byte) (int, error) {
	deadline := time.Now().Add(b.idle)
	if deadline.After(b.end) {
		deadline = b.end
	}
	if err := b.rc.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

// streamErr prefers the server's reason for ending a stream over the send
// error it caused.
func (h *Handler) streamErr(stream pb.DRPCFileService_UploadClient, err error) error {
	if _, rerr := stream.CloseAndRecv(); rerr != nil {
		return rerr
	}
	return err
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	mode := downloadAttachment
	if strings.HasSuffix(r.URL.Path, "/inline") {
		mode = downloadInline
	}
	h.sendDownload(w, r, h.call(r), mode, func(err error) { h.fail(w, r, err) })
}

type downloadMode int

const (
	downloadAttachment downloadMode = iota
	// downloadInline shows a picture or plays a voice message in place.
	downloadInline
	// downloadAPI hands an app the file with its type: an app sends the
	// token in a header, which a page cannot make a browser send.
	downloadAPI
)

// sendDownload streams the attachment of the request's {id} to w, on behalf
// of call.
func (h *Handler) sendDownload(w http.ResponseWriter, r *http.Request, call drpc.Call, mode downloadMode, fail func(error)) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		fail(errNoPage)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(h.cfg.HTTP.TransferTimeout))
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.HTTP.TransferTimeout)
	defer cancel()
	stream, err := h.client.Files.Download(h.client.Stream(ctx, call), &pb.DownloadRequest{AttachmentId: id})
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = stream.Close() }()
	first, err := stream.Recv()
	if err != nil {
		fail(err)
		return
	}
	att := first.GetHeader()
	if att == nil {
		fail(ierrors.Internal(errors.New("download began without a header")))
		return
	}
	// Pictures show in place and voice messages play there; anything else
	// only downloads.
	inPlace := render.IsImage(att.ContentType) || (att.Voice && audio.Playable(att.ContentType))
	if mode == downloadInline && !inPlace {
		fail(errNoPage)
		return
	}
	hd := w.Header()
	disposition := "attachment"
	ctype := "application/octet-stream"
	switch mode {
	case downloadInline:
		disposition, ctype = "inline", att.ContentType
	case downloadAPI:
		ctype = att.ContentType
	}
	hd.Set("Content-Type", ctype)
	cd := mime.FormatMediaType(disposition, map[string]string{"filename": att.Name})
	if cd == "" {
		cd = disposition
	}
	hd.Set("Content-Disposition", cd)
	hd.Set("Content-Length", strconv.FormatInt(att.Size, 10))
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// A download is kept out of the browser's cache, where it would outlive
	// the access to it; a preview in a channel, or in an app, is kept
	// briefly.
	if mode != downloadAttachment {
		hd.Set("Cache-Control", "private, max-age=300")
	} else {
		hd.Set("Cache-Control", "private, no-store")
	}
	w.WriteHeader(http.StatusOK)
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			h.logger.Debug("download cut short", zap.Error(err))
			return
		}
		if _, err := w.Write(msg.GetChunk()); err != nil {
			return
		}
	}
}
