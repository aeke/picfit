package backend

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/cenkalti/dominantcolor"
	"github.com/disintegration/imaging"

	imagefile "github.com/thoas/picfit/image"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

type (
	imageTransformation func(img image.Image) *image.NRGBA
	transformation      func(img image.Image, width int, height int, filter imaging.ResampleFilter) *image.NRGBA
)

var (
	flipTransformations = map[string]imageTransformation{
		"h": imaging.FlipH,
		"v": imaging.FlipV,
	}

	rotateTransformations = map[int]imageTransformation{
		90:  imaging.Rotate90,
		270: imaging.Rotate270,
		180: imaging.Rotate180,
	}
)

type Hex string

type RGB struct {
	Red   uint8
	Green uint8
	Blue  uint8
}

type GoImage struct{}

func (h Hex) toRGB() (RGB, error) {
	return Hex2RGB(h)
}

func Hex2RGB(hex Hex) (RGB, error) {
	var rgb RGB
	values, err := strconv.ParseUint(string(hex), 16, 32)

	if err != nil {
		return RGB{}, err
	}

	rgb = RGB{
		Red:   uint8(values >> 16),
		Green: uint8((values >> 8) & 0xFF),
		Blue:  uint8(values & 0xFF),
	}

	return rgb, nil
}

func (e *GoImage) String() string {
	return "goimage"
}
func (e *GoImage) Resize(img *imagefile.ImageFile, options *Options) ([]byte, error) {
	return e.resize(img, options, imaging.Resize)
}

func (e *GoImage) Thumbnail(img *imagefile.ImageFile, options *Options) ([]byte, error) {
	return e.resize(img, options, imaging.Thumbnail)
}

func (e *GoImage) Rotate(img *imagefile.ImageFile, options *Options) ([]byte, error) {
	image, err := e.source(img)
	if err != nil {
		return nil, err
	}

	deg := options.Degree

	transform, ok := rotateTransformations[deg]
	if !ok {
		return nil, fmt.Errorf("Invalid rotate transformation degree=%d is not supported", deg)
	}

	return e.toBytes(transform(image), options.Format, options.Quality)
}

func (e *GoImage) Flip(img *imagefile.ImageFile, options *Options) ([]byte, error) {
	image, err := e.source(img)
	if err != nil {
		return nil, err
	}

	pos := options.Position

	transform, ok := flipTransformations[pos]
	if !ok {
		return nil, fmt.Errorf("Invalid flip transformation, %s is not supported", pos)
	}

	return e.toBytes(transform(image), options.Format, options.Quality)
}

func (e *GoImage) Fit(img *imagefile.ImageFile, options *Options) ([]byte, error) {
	if options.Format == imaging.GIF {
		content, err := e.transformGIF(img, options, imaging.Thumbnail)
		if err != nil {
			return nil, err
		}

		return content, nil
	}

	image, err := e.source(img)
	if err != nil {
		return nil, err
	}

	return e.transform(image, options, imaging.Fit)
}

func (e *GoImage) toBytes(img image.Image, format imaging.Format, quality int) ([]byte, error) {
	buf := &bytes.Buffer{}

	var err error

	err = encode(buf, img, format, quality)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (e *GoImage) transformGIF(img *imagefile.ImageFile, options *Options, trans transformation) ([]byte, error) {
	first, err := gif.Decode(bytes.NewReader(img.Source))
	if err != nil {
		return nil, err
	}

	factor := scalingFactorImage(first, options.Width, options.Height)
	if factor > 1 && !options.Upscale {
		return img.Source, nil
	}

	g, err := gif.DecodeAll(bytes.NewReader(img.Source))
	if err != nil {
		return nil, err
	}

	firstFrame := g.Image[0].Bounds()
	b := image.Rect(0, 0, firstFrame.Dx(), firstFrame.Dy())
	im := image.NewRGBA(b)

	for i, frame := range g.Image {
		bounds := frame.Bounds()
		draw.Draw(im, bounds, frame, bounds.Min, draw.Over)
		g.Image[i] = imageToPaletted(scale(im, options, trans))
	}

	srcW, srcH := imageSize(first)

	if options.Width == 0 {
		tmpW := float64(options.Height) * float64(srcW) / float64(srcH)
		options.Width = int(math.Max(1.0, math.Floor(tmpW+0.5)))
	}
	if options.Height == 0 {
		tmpH := float64(options.Width) * float64(srcH) / float64(srcW)
		options.Height = int(math.Max(1.0, math.Floor(tmpH+0.5)))
	}

	g.Config.Height = options.Height
	g.Config.Width = options.Width

	buf := bytes.Buffer{}

	err = gif.EncodeAll(&buf, g)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (e *GoImage) resize(img *imagefile.ImageFile, options *Options, trans transformation) ([]byte, error) {
	if options.Format == imaging.GIF {
		content, err := e.transformGIF(img, options, trans)
		if err != nil {
			return nil, err
		}

		return content, nil
	}

	image, err := e.source(img)
	if err != nil {
		return nil, err
	}

	return e.transform(image, options, trans)
}

func (e *GoImage) transform(img image.Image, options *Options, trans transformation) ([]byte, error) {
	return e.toBytes(scale(img, options, trans), options.Format, options.Quality)
}

func (e *GoImage) source(img *imagefile.ImageFile) (image.Image, error) {
	return decode(bytes.NewReader(img.Source))
}

func scalingFactor(srcWidth int, srcHeight int, destWidth int, destHeight int) float64 {
	return math.Max(float64(destWidth)/float64(srcWidth), float64(destHeight)/float64(srcHeight))
}

func scalingFactorImage(img image.Image, dstWidth int, dstHeight int) float64 {
	width, height := imageSize(img)

	return scalingFactor(width, height, dstWidth, dstHeight)
}

func imageSize(e image.Image) (int, int) {
	return e.Bounds().Max.X, e.Bounds().Max.Y
}

func scale(img image.Image, options *Options, trans transformation) image.Image {
	factor := scalingFactorImage(img, options.Width, options.Height)

	if factor < 1 || options.Upscale {
		return trans(img, options.Width, options.Height, imaging.Lanczos)
	}

	return img
}

func imageToPaletted(img image.Image) *image.Paletted {
	b := img.Bounds()
	pm := image.NewPaletted(b, palette.Plan9)
	draw.FloydSteinberg.Draw(pm, b, img, image.ZP)
	return pm
}

func FindDominantColor(img image.Image) string {
	return dominantcolor.Hex(dominantcolor.Find(img))
}

func FindLuminenace(items []float32) (result float32) {
	for _, v := range items {
		v = v / 255
		if v <= 0.03928 {
			return v / 12.92
		} else {
			return ((v + 0.55) / 1.055) * 2
		}
	}
	result = (items[0]*0.2126 + items[1]*0.7152 + items[2]*0.07222)
	return
}
func createWatermark(base image.Image) (outImage image.Image) {
	var _ RGB
	var hex = Hex(strings.Replace(FindDominantColor(base), "#", "", 1))
	rgb, _ := Hex2RGB(hex)

	var markPath = "watermark.png"
	var list []float32
	list = append(list, float32(rgb.Red))
	list = append(list, float32(rgb.Green))
	list = append(list, float32(rgb.Blue))
	var status = (FindLuminenace(list))

	if status > 1.90 {
		markPath = "watermark_colored.png"
	}

	fmt.Print("\n\n")
	fmt.Print("\n\n")
	fmt.Print(status)
	fmt.Print(rgb)
	baseFile, _ := os.Open(markPath)
	defer baseFile.Close()
	mark, _ := png.Decode(baseFile)
	baseBound := base.Bounds()
	markBound := mark.Bounds()
	offset := image.Pt(
		(baseBound.Size().X/2)-(markBound.Size().X/2),
		(baseBound.Size().Y/2)-(markBound.Size().Y/2))

	outputImage := image.NewRGBA(baseBound)
	draw.Draw(outputImage, outputImage.Bounds(), base, image.ZP, draw.Src)
	draw.DrawMask(outputImage, mark.Bounds().Add(offset), mark, image.ZP, image.NewUniform(color.Alpha{64}), image.ZP, draw.Over)
	outImage = outputImage

	return
}

func encode(w io.Writer, img image.Image, format imaging.Format, quality int) error {
	var err error
	switch format {
	case imaging.JPEG:
		var rgba *image.RGBA
		if nrgba, ok := img.(*image.NRGBA); ok {
			if nrgba.Opaque() {
				rgba = &image.RGBA{
					Pix:    nrgba.Pix,
					Stride: nrgba.Stride,
					Rect:   nrgba.Rect,
				}
			}
		}
		if rgba != nil {
			err = jpeg.Encode(w, createWatermark(rgba), &jpeg.Options{Quality: quality})
		} else {
			err = jpeg.Encode(w, createWatermark(img), &jpeg.Options{Quality: quality})
		}

	case imaging.PNG:
		err = png.Encode(w, img)
	case imaging.GIF:
		err = gif.Encode(w, img, &gif.Options{NumColors: 256})
	case imaging.TIFF:
		err = tiff.Encode(w, img, &tiff.Options{Compression: tiff.Deflate, Predictor: true})
	case imaging.BMP:
		err = bmp.Encode(w, img)
	default:
		err = imaging.ErrUnsupportedFormat
	}
	return err
}
