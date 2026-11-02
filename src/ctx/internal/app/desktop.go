package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	systemsurface "github.com/webong/ext/res/system/capture/surface"
)

func desktopCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: desktop needs capture or input")
		return 2
	}
	action := args[0]
	request, err := parseDesktopArgs(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 2
	}
	if action != "capture" && action != "input" {
		fmt.Fprintf(stderr, "ctx: unknown desktop action %s\n", action)
		return 2
	}
	if request.window == "" {
		fmt.Fprintln(stderr, "ctx: desktop needs --window with an observed window ID")
		return 2
	}
	surfaces, err := systemsurface.DiscoverSurfaces(context.Background(), systemsurface.SurfaceOptions{})
	if err != nil {
		return reportError(stderr, err)
	}
	var target *systemsurface.WindowInfo
	for index := range surfaces.Windows {
		if surfaces.Windows[index].ID == request.window {
			target = &surfaces.Windows[index]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(stderr, "ctx: window %s is not currently observed (%s)\n", request.window, surfaces.WindowStatus.State)
		return 1
	}
	switch action {
	case "capture":
		return desktopCapture(*target, request, stdout, stderr)
	default:
		return desktopInput(*target, request, stdout, stderr)
	}
}

type desktopRequest struct {
	window     string
	out        string
	input      systemsurface.SurfaceInput
	hasInput   bool
	inputCount int
	maxBytes   int
	maxPixels  int
	maxRunes   int
	timeout    time.Duration
	jsonOutput bool
}

func parseDesktopArgs(args []string) (desktopRequest, error) {
	request := desktopRequest{timeout: 5 * time.Second}
	modifiers := []string{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return "", fmt.Errorf("%s needs a value", name)
			}
			index++
			return args[index], nil
		}
		var err error
		switch name {
		case "--window":
			request.window, err = value()
		case "--out":
			request.out, err = value()
		case "--text":
			var text string
			text, err = value()
			if err == nil {
				request.setInput(systemsurface.SurfaceInput{Kind: systemsurface.SurfaceInputText, Text: text})
			}
		case "--key":
			var key string
			key, err = value()
			if err == nil {
				request.setInput(systemsurface.SurfaceInput{Kind: systemsurface.SurfaceInputKey, Key: key})
			}
		case "--modifier":
			var modifier string
			modifier, err = value()
			if err == nil {
				modifiers = append(modifiers, modifier)
			}
		case "--move":
			var point systemsurface.SurfacePoint
			point, err = parseDesktopPoint(value)
			if err == nil {
				request.setInput(systemsurface.SurfaceInput{Kind: systemsurface.SurfaceInputMove, Point: point})
			}
		case "--click":
			var point systemsurface.SurfacePoint
			point, err = parseDesktopPoint(value)
			if err == nil {
				request.setInput(systemsurface.SurfaceInput{Kind: systemsurface.SurfaceInputClick, Point: point})
			}
		case "--drag":
			var raw string
			raw, err = value()
			if err == nil {
				start, end, parseErr := parseDesktopDrag(raw)
				err = parseErr
				if err == nil {
					request.setInput(systemsurface.SurfaceInput{Kind: systemsurface.SurfaceInputDrag, Point: start, End: end})
				}
			}
		case "--scroll":
			var raw string
			raw, err = value()
			if err == nil {
				point, deltaX, deltaY, parseErr := parseDesktopScroll(raw)
				err = parseErr
				if err == nil {
					request.setInput(systemsurface.SurfaceInput{
						Kind: systemsurface.SurfaceInputScroll, Point: point,
						DeltaX: deltaX, DeltaY: deltaY,
					})
				}
			}
		case "--button":
			var button string
			button, err = value()
			if err == nil {
				request.input.Button = button
			}
		case "--max-pixels":
			var raw string
			raw, err = value()
			if err == nil {
				request.maxPixels, err = strconv.Atoi(raw)
			}
		case "--max-bytes":
			var raw string
			raw, err = value()
			if err == nil {
				request.maxBytes, err = strconv.Atoi(raw)
			}
		case "--max-text-runes":
			var raw string
			raw, err = value()
			if err == nil {
				request.maxRunes, err = strconv.Atoi(raw)
			}
		case "--timeout":
			var raw string
			raw, err = value()
			if err == nil {
				request.timeout, err = time.ParseDuration(raw)
			}
		case "--json":
			if hasInline {
				err = fmt.Errorf("--json takes no value")
			} else {
				request.jsonOutput = true
			}
		default:
			err = fmt.Errorf("unknown desktop option %s", name)
		}
		if err != nil {
			return request, err
		}
	}
	if len(modifiers) > 0 {
		if request.input.Kind != systemsurface.SurfaceInputKey {
			return request, fmt.Errorf("--modifier needs --key")
		}
		request.input.Modifiers = modifiers
	}
	if request.inputCount > 1 {
		return request, fmt.Errorf("choose exactly one input operation")
	}
	if request.out != "" && request.hasInput {
		return request, fmt.Errorf("--out only applies to capture")
	}
	if request.input.Button != "" && request.input.Kind != systemsurface.SurfaceInputClick && request.input.Kind != systemsurface.SurfaceInputDrag {
		return request, fmt.Errorf("--button applies to --click or --drag")
	}
	return request, nil
}

func (r *desktopRequest) setInput(input systemsurface.SurfaceInput) {
	r.input = input
	r.hasInput = true
	r.inputCount++
}

func parseDesktopPoint(value func() (string, error)) (systemsurface.SurfacePoint, error) {
	raw, err := value()
	if err != nil {
		return systemsurface.SurfacePoint{}, err
	}
	x, y, err := parseDesktopPair(raw)
	if err != nil {
		return systemsurface.SurfacePoint{}, err
	}
	return systemsurface.SurfacePoint{X: x, Y: y}, nil
}

func parseDesktopPair(raw string) (int, int, error) {
	left, right, ok := strings.Cut(raw, ",")
	if !ok {
		return 0, 0, fmt.Errorf("expected X,Y")
	}
	x, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid X coordinate")
	}
	y, err := strconv.Atoi(strings.TrimSpace(right))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Y coordinate")
	}
	return x, y, nil
}

func parseDesktopDrag(raw string) (systemsurface.SurfacePoint, systemsurface.SurfacePoint, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return systemsurface.SurfacePoint{}, systemsurface.SurfacePoint{}, fmt.Errorf("--drag needs X1,Y1,X2,Y2")
	}
	x1, y1, err := parseDesktopPair(parts[0] + "," + parts[1])
	if err != nil {
		return systemsurface.SurfacePoint{}, systemsurface.SurfacePoint{}, err
	}
	x2, y2, err := parseDesktopPair(parts[2] + "," + parts[3])
	start := systemsurface.SurfacePoint{X: x1, Y: y1}
	end := systemsurface.SurfacePoint{X: x2, Y: y2}
	return start, end, err
}

func parseDesktopScroll(raw string) (systemsurface.SurfacePoint, int, int, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return systemsurface.SurfacePoint{}, 0, 0, fmt.Errorf("--scroll needs X,Y,DELTAX,DELTAY")
	}
	x, y, err := parseDesktopPair(parts[0] + "," + parts[1])
	if err != nil {
		return systemsurface.SurfacePoint{}, 0, 0, err
	}
	point := systemsurface.SurfacePoint{X: x, Y: y}
	deltaX, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return point, 0, 0, fmt.Errorf("invalid horizontal delta")
	}
	deltaY, err := strconv.Atoi(strings.TrimSpace(parts[3]))
	if err != nil {
		return point, 0, 0, fmt.Errorf("invalid vertical delta")
	}
	return point, deltaX, deltaY, nil
}

func desktopCapture(target systemsurface.WindowInfo, request desktopRequest, stdout, stderr io.Writer) int {
	if request.out == "" {
		fmt.Fprintln(stderr, "ctx: desktop capture needs --out (a file path or - for stdout)")
		return 2
	}
	options := systemsurface.CaptureOptions{MaxBytes: request.maxBytes, MaxPixels: request.maxPixels, Timeout: request.timeout}
	capture, err := systemsurface.CaptureWindow(context.Background(), target, options)
	if err != nil {
		return reportError(stderr, err)
	}
	summary := struct {
		Window     string    `json:"window"`
		Format     string    `json:"format"`
		Width      int       `json:"width"`
		Height     int       `json:"height"`
		Bytes      int       `json:"bytes"`
		Path       string    `json:"path"`
		CapturedAt time.Time `json:"captured_at"`
	}{target.ID, capture.Format, capture.Width, capture.Height, len(capture.Data), request.out, capture.CapturedAt}
	if request.out == "-" {
		if _, err := stdout.Write(capture.Data); err != nil {
			return reportError(stderr, err)
		}
		if request.jsonOutput {
			return writeJSON(stderr, stderr, summary)
		}
		return 0
	}
	if err := os.WriteFile(request.out, capture.Data, 0o600); err != nil {
		return reportError(stderr, err)
	}
	if request.jsonOutput {
		return writeJSON(stdout, stderr, summary)
	}
	fmt.Fprintf(stdout, "captured %dx%d %s to %s\n", capture.Width, capture.Height, capture.Format, request.out)
	return 0
}

func desktopInput(target systemsurface.WindowInfo, request desktopRequest, stdout, stderr io.Writer) int {
	if !request.hasInput {
		fmt.Fprintln(stderr, "ctx: desktop input needs --text, --key, --move, --click, --drag, or --scroll")
		return 2
	}
	options := systemsurface.InputOptions{MaxTextRunes: request.maxRunes, Timeout: request.timeout}
	if err := systemsurface.SendWindowInput(context.Background(), target, request.input, options); err != nil {
		return reportError(stderr, err)
	}
	summary := struct {
		Window string `json:"window"`
		Kind   string `json:"kind"`
	}{target.ID, string(request.input.Kind)}
	if request.jsonOutput {
		return writeJSON(stdout, stderr, summary)
	}
	fmt.Fprintf(stdout, "sent %s to %s\n", request.input.Kind, target.ID)
	return 0
}
