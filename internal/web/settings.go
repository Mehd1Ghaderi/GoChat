package web

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"image/png"
	"net/http"
	"strings"

	"github.com/pquerna/otp"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
)

type settingsData struct {
	Page
	Email string
}

type totpData struct {
	User  *pb.User
	Error string
}

type totpSetupData struct {
	Secret string
	URL    string
	QR     template.URL
	Error  string
}

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	me, err := h.client.Auth.Me(ctx, &pb.MeRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.page(w, r, http.StatusOK, "settings", settingsData{
		Page:  Page{Title: "Settings", CSRF: h.csrfToken(w, r), User: me.User},
		Email: me.Email,
	})
}

func (h *Handler) status(w http.ResponseWriter, err error, ok string) {
	if err != nil {
		h.text(w, http.StatusOK, "error", drpc.Err(err).Message)
		return
	}
	h.text(w, http.StatusOK, "ok", ok)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Users.UpdateProfile(ctx, &pb.UpdateProfileRequest{DisplayName: r.PostFormValue("display_name")})
	h.me.Delete(h.sessionToken(r))
	h.status(w, err, "Saved.")
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	if r.PostFormValue("new") != r.PostFormValue("confirm") {
		h.text(w, http.StatusOK, "error", "The new passwords do not match.")
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Auth.ChangePassword(ctx, &pb.ChangePasswordRequest{CurrentPassword: r.PostFormValue("current"), NewPassword: r.PostFormValue("new")})
	h.status(w, err, "Password changed. Other sessions were signed out.")
}

func qrCode(url string) (template.URL, error) {
	key, err := otp.NewKeyFromURL(url)
	if err != nil {
		return "", err
	}
	img, err := key.Image(200, 200)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	// A PNG generated here, never input: safe as an image source.
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())), nil //nolint:gosec
}

func (h *Handler) totpFragment(w http.ResponseWriter, r *http.Request, errMsg string) {
	ctx, cancel := h.ctx(r)
	defer cancel()
	me, err := h.client.Auth.Me(ctx, &pb.MeRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "totp", totpData{User: me.User, Error: errMsg})
}

func (h *Handler) beginTOTP(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Auth.BeginTOTPSetup(ctx, &pb.BeginTOTPSetupRequest{Password: r.PostFormValue("password")})
	if err != nil {
		h.totpFragment(w, r, drpc.Err(err).Message)
		return
	}
	qr, err := qrCode(res.Url)
	if err != nil {
		h.totpFragment(w, r, "Could not draw the QR code; type the key instead.")
		return
	}
	h.fragment(w, http.StatusOK, "totp-setup", totpSetupData{Secret: res.Secret, URL: res.Url, QR: qr})
}

func (h *Handler) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Auth.ConfirmTOTPSetup(ctx, &pb.ConfirmTOTPSetupRequest{Code: r.PostFormValue("code")})
	if err != nil {
		url := r.PostFormValue("url")
		data := totpSetupData{Secret: r.PostFormValue("secret"), URL: url, Error: drpc.Err(err).Message}
		if strings.HasPrefix(url, "otpauth://totp/") {
			if qr, qerr := qrCode(url); qerr == nil {
				data.QR = qr
			}
		}
		if data.QR == "" {
			h.totpFragment(w, r, data.Error)
			return
		}
		h.fragment(w, http.StatusOK, "totp-setup", data)
		return
	}
	h.me.Delete(h.sessionToken(r))
	h.fragment(w, http.StatusOK, "totp-codes", res.RecoveryCodes)
}

func (h *Handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Auth.DisableTOTP(ctx, &pb.DisableTOTPRequest{Password: r.PostFormValue("password"), Code: r.PostFormValue("code")})
	h.me.Delete(h.sessionToken(r))
	msg := ""
	if err != nil {
		msg = drpc.Err(err).Message
	}
	h.totpFragment(w, r, msg)
}
