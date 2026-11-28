package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// Crop produces a new PNG using image-local coordinates. The returned frame
// uses crop-local coordinates and does not rewrite the source bytes.
func Crop(frame Frame, region Region, maxBytes, maxPixels int) (Frame, error) {
	img, err := decodeFrame(frame, maxBytes, maxPixels)
	if err != nil {
		return Frame{}, err
	}
	bounds := img.Bounds()
	if region.X < 0 || region.Y < 0 || region.Width < 1 || region.Height < 1 ||
		region.X > bounds.Dx()-region.Width || region.Y > bounds.Dy()-region.Height {
		return Frame{}, errors.New("crop exceeds image bounds")
	}
	output := image.NewRGBA(image.Rect(0, 0, region.Width, region.Height))
	draw.Draw(output, output.Bounds(), img, image.Pt(bounds.Min.X+region.X, bounds.Min.Y+region.Y), draw.Src)
	var data bytes.Buffer
	if err = png.Encode(&data, output); err != nil {
		return Frame{}, err
	}
	if data.Len() > maxBytes {
		return Frame{}, errors.New("cropped frame exceeds byte ceiling")
	}
	frame.Data = data.Bytes()
	frame.CoordinateSpace = "crop-local-pixels"
	return frame, nil
}

type Annotation struct {
	ID     string     `json:"id"`
	Region Region     `json:"region"`
	Color  color.RGBA `json:"color"`
}

// Annotate draws bounded element boxes without mutating the source frame.
func Annotate(frame Frame, boxes []Annotation, maxBytes, maxPixels int) ([]byte, error) {
	if len(boxes) > 500 {
		return nil, errors.New("annotation count exceeds ceiling")
	}
	img, err := decodeFrame(frame, maxBytes, maxPixels)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	output := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(output, output.Bounds(), img, bounds.Min, draw.Src)
	seen := map[string]bool{}
	for _, box := range boxes {
		region := box.Region
		if box.ID == "" || len(box.ID) > 256 || seen[box.ID] || region.X < 0 || region.Y < 0 ||
			region.Width < 1 || region.Height < 1 || region.X > bounds.Dx()-region.Width ||
			region.Y > bounds.Dy()-region.Height {
			return nil, errors.New("invalid annotation ID or region")
		}
		seen[box.ID] = true
		for x := region.X; x < region.X+region.Width; x++ {
			output.SetRGBA(x, region.Y, box.Color)
			output.SetRGBA(x, region.Y+region.Height-1, box.Color)
		}
		for y := region.Y; y < region.Y+region.Height; y++ {
			output.SetRGBA(region.X, y, box.Color)
			output.SetRGBA(region.X+region.Width-1, y, box.Color)
		}
	}
	var data bytes.Buffer
	if err = png.Encode(&data, output); err != nil {
		return nil, err
	}
	if data.Len() > maxBytes {
		return nil, errors.New("annotated image exceeds byte ceiling")
	}
	return data.Bytes(), nil
}

type ContactSheetOptions struct {
	Columns       int
	TileWidth     int
	TileHeight    int
	MaxFrameBytes int
	MaxTotalBytes int64
	MaxPixels     int
}

// ContactSheet creates a deterministic finite PNG. Labels and other
// presentation choices remain consumer concerns.
func ContactSheet(frames []Frame, options ContactSheetOptions) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 256 || options.Columns < 1 || options.Columns > len(frames) ||
		options.TileWidth < 1 || options.TileHeight < 1 || options.TileWidth > 4_096 ||
		options.TileHeight > 4_096 || options.MaxFrameBytes < 1 ||
		options.MaxFrameBytes > maximumMediaBytes || options.MaxTotalBytes < int64(options.MaxFrameBytes) ||
		options.MaxTotalBytes > 1<<30 || options.MaxPixels < 1 || options.MaxPixels > 16<<20 {
		return nil, errors.New("invalid contact sheet bounds")
	}
	rows := (len(frames) + options.Columns - 1) / options.Columns
	width, height := options.Columns*options.TileWidth, rows*options.TileHeight
	if width > options.MaxPixels/height {
		return nil, errors.New("contact sheet exceeds pixel ceiling")
	}
	output := image.NewRGBA(image.Rect(0, 0, width, height))
	var totalBytes int64
	for index, frame := range frames {
		if totalBytes+int64(len(frame.Data)) > options.MaxTotalBytes {
			return nil, errors.New("contact sheet exceeds input byte ceiling")
		}
		totalBytes += int64(len(frame.Data))
		img, err := decodeFrame(frame, options.MaxFrameBytes, options.MaxPixels)
		if err != nil {
			return nil, err
		}
		bounds := img.Bounds()
		x0, y0 := (index%options.Columns)*options.TileWidth, (index/options.Columns)*options.TileHeight
		for y := 0; y < options.TileHeight; y++ {
			for x := 0; x < options.TileWidth; x++ {
				output.Set(x0+x, y0+y, img.At(
					bounds.Min.X+x*bounds.Dx()/options.TileWidth,
					bounds.Min.Y+y*bounds.Dy()/options.TileHeight,
				))
			}
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, output); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}
