// Package perception defines transport-free content interpretation and image
// observation contracts. Providers decide which modalities they can handle;
// callers decide what content may be sent to them.
package perception

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"mime"
	"strings"
	"unicode/utf8"
)

const APIVersion = "intelligence.perception/v1"

var ErrInvalid = errors.New("invalid perception value")

const (
	maxParts        = 16
	maxInputBytes   = 10 << 20
	maxOutputBytes  = 4 << 20
	maxFindings     = 128
	maxObservations = 1024
	maxMaskBytes    = 8 << 20
)

// Part is caller-supplied content. No field is an implicit path to open.
type Part struct {
	Kind      string `json:"kind"` // text, image, audio, or video
	Text      string `json:"text,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

type InterpretRequest struct {
	APIVersion string `json:"apiVersion"`
	RequestID  string `json:"requestId,omitempty"`
	Model      string `json:"model"`
	Parts      []Part `json:"parts"`
	Prompt     string `json:"prompt,omitempty"`
}

// Finding preserves the source part indexes for model-produced claims.
type Finding struct {
	Kind        string   `json:"kind"`
	Text        string   `json:"text"`
	SourceParts []int    `json:"sourceParts"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

type InterpretResponse struct {
	APIVersion string    `json:"apiVersion"`
	RequestID  string    `json:"requestId,omitempty"`
	Findings   []Finding `json:"findings"`
}

type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Observation describes a detected region. Mask is a base64 PNG, when set.
type Observation struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	Region     Region  `json:"region"`
	Mask       string  `json:"mask,omitempty"`
	Evidence   string  `json:"evidence,omitempty"`
}

type ObserveRequest struct {
	APIVersion string `json:"apiVersion"`
	RequestID  string `json:"requestId,omitempty"`
	Model      string `json:"model"`
	MediaType  string `json:"mediaType"`
	Image      []byte `json:"image"`
	Prompt     string `json:"prompt,omitempty"`
}

type ObserveResponse struct {
	APIVersion   string        `json:"apiVersion"`
	RequestID    string        `json:"requestId,omitempty"`
	Observations []Observation `json:"observations"`
}

// Info is the provider's declared capabilities, not a claim about every model.
type Info struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
}

type Provider interface {
	Info(context.Context) (Info, error)
	Close() error
}

type Interpreter interface {
	Interpret(context.Context, InterpretRequest) (InterpretResponse, error)
}

type Observer interface {
	Observe(context.Context, ObserveRequest) (ObserveResponse, error)
}

func validID(id string) bool { return len(id) <= 128 && utf8.ValidString(id) }

func (r InterpretRequest) Validate() error {
	if r.APIVersion != APIVersion || !validID(r.RequestID) || strings.TrimSpace(r.Model) == "" || len(r.Model) > 256 || !utf8.ValidString(r.Model) || len(r.Prompt) > 16<<10 || !utf8.ValidString(r.Prompt) || len(r.Parts) == 0 || len(r.Parts) > maxParts {
		return fmt.Errorf("%w: invalid interpretation envelope", ErrInvalid)
	}
	size := len(r.Prompt)
	for i, p := range r.Parts {
		size += len(p.Text) + len(p.Data)
		if size > maxInputBytes {
			return fmt.Errorf("%w: interpretation input exceeds %d bytes", ErrInvalid, maxInputBytes)
		}
		switch p.Kind {
		case "text":
			if strings.TrimSpace(p.Text) == "" || !utf8.ValidString(p.Text) || len(p.Data) != 0 || p.MediaType != "" {
				return fmt.Errorf("%w: invalid text part %d", ErrInvalid, i)
			}
		case "image", "audio", "video":
			mediaType, _, err := mime.ParseMediaType(p.MediaType)
			if p.Text != "" || len(p.Data) == 0 || err != nil || !strings.HasPrefix(mediaType, p.Kind+"/") {
				return fmt.Errorf("%w: invalid %s part %d", ErrInvalid, p.Kind, i)
			}
		default:
			return fmt.Errorf("%w: unsupported part kind %q", ErrInvalid, p.Kind)
		}
	}
	return nil
}

func (r InterpretResponse) Validate(request InterpretRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if r.APIVersion != APIVersion || !validID(r.RequestID) || (request.RequestID != "" && r.RequestID != request.RequestID) || r.Findings == nil || len(r.Findings) > maxFindings {
		return fmt.Errorf("%w: invalid interpretation response envelope", ErrInvalid)
	}
	size := 0
	for i, finding := range r.Findings {
		size += len(finding.Kind) + len(finding.Text)
		if size > maxOutputBytes || strings.TrimSpace(finding.Kind) == "" || strings.TrimSpace(finding.Text) == "" || !utf8.ValidString(finding.Kind) || !utf8.ValidString(finding.Text) || len(finding.SourceParts) == 0 || len(finding.SourceParts) > len(request.Parts) {
			return fmt.Errorf("%w: invalid finding %d", ErrInvalid, i)
		}
		seen := make(map[int]bool, len(finding.SourceParts))
		for _, source := range finding.SourceParts {
			if source < 0 || source >= len(request.Parts) || seen[source] {
				return fmt.Errorf("%w: invalid finding %d source", ErrInvalid, i)
			}
			seen[source] = true
		}
		if finding.Confidence != nil && (!finite(*finding.Confidence) || *finding.Confidence < 0 || *finding.Confidence > 1) {
			return fmt.Errorf("%w: invalid finding %d confidence", ErrInvalid, i)
		}
	}
	return nil
}

func (r ObserveRequest) Validate() error {
	mediaType, _, mediaErr := mime.ParseMediaType(r.MediaType)
	if r.APIVersion != APIVersion || !validID(r.RequestID) || strings.TrimSpace(r.Model) == "" || len(r.Model) > 256 || !utf8.ValidString(r.Model) || mediaErr != nil || !strings.HasPrefix(mediaType, "image/") || len(r.Image) == 0 || len(r.Image) > maxInputBytes || len(r.Prompt) > 16<<10 || !utf8.ValidString(r.Prompt) {
		return fmt.Errorf("%w: invalid observation request", ErrInvalid)
	}
	return nil
}

func (r ObserveResponse) Validate(request ObserveRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if r.APIVersion != APIVersion || !validID(r.RequestID) || (request.RequestID != "" && r.RequestID != request.RequestID) || r.Observations == nil || len(r.Observations) > maxObservations {
		return fmt.Errorf("%w: invalid observation response envelope", ErrInvalid)
	}
	for i, o := range r.Observations {
		if strings.TrimSpace(o.Kind) == "" || strings.TrimSpace(o.Label) == "" || !utf8.ValidString(o.Kind) || !utf8.ValidString(o.Label) || !utf8.ValidString(o.Evidence) || len(o.Evidence) > 1<<20 || !finite(o.Confidence) || o.Confidence < 0 || o.Confidence > 1 || !o.Region.valid() {
			return fmt.Errorf("%w: invalid observation %d", ErrInvalid, i)
		}
		if o.Mask != "" {
			if len(o.Mask) > base64.StdEncoding.EncodedLen(maxMaskBytes) {
				return fmt.Errorf("%w: observation %d mask exceeds limit", ErrInvalid, i)
			}
			decoded, err := base64.StdEncoding.DecodeString(o.Mask)
			if err != nil || len(decoded) > maxMaskBytes || len(decoded) < 8 || string(decoded[:8]) != "\x89PNG\r\n\x1a\n" {
				return fmt.Errorf("%w: observation %d mask is not a PNG", ErrInvalid, i)
			}
		}
	}
	return nil
}

func (r Region) valid() bool {
	return finite(r.X) && finite(r.Y) && finite(r.Width) && finite(r.Height) && r.X >= 0 && r.Y >= 0 && r.Width >= 0 && r.Height >= 0
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (i Info) Validate() error {
	if strings.TrimSpace(i.ID) == "" || !utf8.ValidString(i.ID) || len(i.ID) > 128 || len(i.Capabilities) > 32 {
		return fmt.Errorf("%w: invalid provider info", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, capability := range i.Capabilities {
		if strings.TrimSpace(capability) == "" || len(capability) > 128 || !utf8.ValidString(capability) || seen[capability] {
			return fmt.Errorf("%w: invalid provider capability", ErrInvalid)
		}
		seen[capability] = true
	}
	return nil
}
