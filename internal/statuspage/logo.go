package statuspage

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Logos are uploaded to and served by the agent itself, so a self-hosted page
// never loads anything from elsewhere. Like the hosted pages there's a logo
// and an optional variant for dark mode.

// Variant is which theme a logo is for.
type Variant string

const (
	Light Variant = "light"
	Dark  Variant = "dark"
)

// ParseVariant checks a variant from a URL.
func ParseVariant(s string) (Variant, bool) {
	v := Variant(s)
	return v, v == Light || v == Dark
}

// MaxLogoBytes is the largest logo accepted.
const MaxLogoBytes = 512 << 10

// Logo is an uploaded image.
type Logo struct {
	Type string `json:"type"` // content type
	Data []byte `json:"data"`
	// Version changes with every upload, so browsers can cache a logo until
	// it's replaced.
	Version int64 `json:"version"`
}

// NewLogo checks an upload. Its type comes from the content, not from what
// the upload claims. SVG is accepted (logos often are SVG); it's served
// sandboxed.
func NewLogo(data []byte) (Logo, error) {
	if len(data) == 0 {
		return Logo{}, errors.New("the image couldn't be read")
	}
	if len(data) > MaxLogoBytes {
		return Logo{}, ErrLogoTooLarge
	}
	typ, err := detectImage(data)
	if err != nil {
		return Logo{}, err
	}
	return Logo{Type: typ, Data: data, Version: time.Now().UnixMilli()}, nil
}

// ErrLogoTooLarge means an upload is over MaxLogoBytes.
var ErrLogoTooLarge = fmt.Errorf("keep the logo under %d KB", MaxLogoBytes>>10)

func detectImage(b []byte) (string, error) {
	switch t := http.DetectContentType(b); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t, nil
	}
	if bytes.Contains(b[:min(len(b), 1024)], []byte("<svg")) {
		return "image/svg+xml", nil
	}
	return "", errors.New("upload a PNG, JPEG, WebP, GIF or SVG image")
}
