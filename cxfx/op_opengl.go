// +build cxfx

package cxfx

import (
	"bufio"
	//"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/skycoin/cx/cx"
)

type Texture struct {
	path   string
	width  int32
	height int32
	level  uint32
	pixels []float32
}

var gifs map[string]*gif.GIF = make(map[string]*gif.GIF, 0)
var textures map[string]Texture = make(map[string]Texture, 0)

func decodeImg(file *os.File, cpuCopy bool) (data []byte, width int32, height int32, pixels []float32) {
	img, _, err := image.Decode(file)
	if err != nil {
		panic(err)
	}

	rgba := image.NewRGBA(img.Bounds())
	if rgba.Stride != rgba.Rect.Size().X*4 {
		panic("unsupported stride")
	}

	draw.Draw(rgba, rgba.Bounds(), img, image.Point{0, 0}, draw.Src)
	data = rgba.Pix
	width = int32(rgba.Rect.Size().X)
	height = int32(rgba.Rect.Size().Y)
	if cpuCopy {
		pixels = make([]float32, width*height*4)
		var x int32
		var y int32
		for y = 0; y < height; y++ {
			yoffset := y * width * 4
			for x = 0; x < width; x++ {
				var xoffset = yoffset + x*4
				color := rgba.At(int(x), int(y))
				r, g, b, a := color.RGBA()
				pixels[xoffset] = float32(r) / 65535.0
				pixels[xoffset+1] = float32(g) / 65535.0
				pixels[xoffset+2] = float32(b) / 65535.0
				pixels[xoffset+3] = float32(a) / 65535.0
			}
		}
	}
	return
}

const (
	HDR_NONE = iota
	HDR_32_RLE_RGBE
	MINLEN = 8
	MAXLEN = 0x7fff
	R      = 0
	G      = 1
	B      = 2
	E      = 3
)

func unpack(file *os.File, width int, line []byte) bool {
	if width < MINLEN || width > MAXLEN {
		return unpack_(file, width, line)
	}

	file.Read(line[:4])
	if line[R] != 2 {
		file.Seek(-4, io.SeekCurrent)
		return unpack_(file, width, line)
	}

	if line[G] != 2 || (line[B]&128) != 0 {
		return unpack_(file, width-1, line[4:])
	}

	var b [1]byte
	for i := 0; i < 4; i++ {
		for j := 0; j < width; {
			file.Read(b[:])
			var count int = int(b[0])
			if count > 128 {
				count &= 127
				file.Read(b[:])
				var value int = int(b[0])
				for c := 0; c < count; c++ {
					line[j+c+i] = byte(value)
				}
			} else {
				for c := 0; c < count; c++ {
					offset := j + c + i
					file.Read(line[offset : offset+1])
				}
			}
		}
	}
	return true
}

func unpack_(file *os.File, width int, line []byte) bool {
	var rshift uint
	var repeat [4]byte
	for width > 0 {
		file.Read(line[0:4])
		if line[R] == 1 && line[G] == 1 && line[B] == 1 {
			for i := line[E] << rshift; i > 0; i-- {
				copy(line[0:4], repeat[:])
				line = line[4:]
				width--
			}
			rshift += 8
		} else {
			copy(repeat[:], line[0:4])
			line = line[4:]
			width--
			rshift = 0
		}
	}
	return true
}

func decodeHdr(file *os.File) (data []byte, iwidth int32, iheight int32) {
	data = nil
	iwidth = 0
	iheight = 0

	var format int
	scanner := bufio.NewScanner(file)

	var pos int64
	scanLines := func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		advance, token, err = bufio.ScanLines(data, atEOF)
		pos += int64(advance)
		return
	}

	scanner.Split(scanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "#?RADIANCE" {
		} else if strings.HasPrefix(line, "#") {
		} else if strings.HasPrefix(line, "FORMAT=") {
			var sformat string
			if n, err := fmt.Sscanf(line, "FORMAT=%s\n", &sformat); n != 1 && err != nil {
				fmt.Printf("Failed to scan format : err '%s'\n", err)
				return
			}
			if sformat == "32-bit_rle_rgbe" {
				format = HDR_32_RLE_RGBE
			}
		} else if len(line) == 0 {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Failed to scan : err %v\n", scanner.Err())
	}

	if format != HDR_32_RLE_RGBE {
		fmt.Printf("Invalid format %d\n", format)
		return
	}

	file.Seek(pos, 0)

	var width int
	var height int
	if n, err := fmt.Fscanf(file, "-Y %d +X %d\n", &width, &height); n != 2 || err != nil {
		fmt.Printf("Failed to scan width and height : err '%s'\n", err)
		return
	}

	iwidth = int32(width)
	iheight = int32(height)

	//var colors []float32 = make([]float32, width*height*3)
	var line []byte = make([]byte, width*4)
	data = make([]byte, width*height*3*4)

	for y := int(0); y < height; y++ {
		if unpack(file, width, line) == false {
			fmt.Printf("Failed to unpack line %d\n", y)
			return
		}

		yoffset := y /*(height - y - 1)*/ * width * 3 * 4
		for x := 0; x < width; x++ {
			loffset := x * 4
			exponent := math.Pow(2.0, float64(int(line[loffset+3])-128))
			xoffset := yoffset + x*3*4
			r := float32(exponent * float64(line[loffset]) / 256.0)
			g := float32(exponent * float64(line[loffset+1]) / 256.0)
			b := float32(exponent * float64(line[loffset+2]) / 256.0)

			WriteMemF32(data, xoffset, r)
			WriteMemF32(data, xoffset+4, g)
			WriteMemF32(data, xoffset+8, b)
		}
	}
	return
}

func uploadTexture(path string, target uint32, level uint32, cpuCopy bool) {
	file, err := CXOpenFile(path)
	defer file.Close()
	if err != nil {
		panic(fmt.Sprintf("texture %q not found on disk: %v\n", path, err))
	}

	ext := filepath.Ext(path)
	var data []byte
	var internalFormat int32
	var inputFormat uint32
	var inputType uint32
	var width int32
	var height int32
	var pixels []float32
	if ext == ".png" || ext == ".jpeg" || ext == ".jpg" {
		internalFormat = cxglRGBA8
		inputFormat = cxglRGBA
		inputType = cxglUNSIGNED_BYTE
		data, width, height, pixels = decodeImg(file, cpuCopy)
		if cpuCopy {
		}
		if len(pixels) > 0 {
			var texture Texture
			texture.pixels = pixels
			texture.width = width
			texture.height = height
			texture.path = path
			texture.level = level
			textures[path] = texture
		}
	} else if ext == ".hdr" {
		internalFormat = cxglRGB16F
		inputFormat = cxglRGB
		inputType = cxglFLOAT
		data, width, height = decodeHdr(file)
	}

	if len(data) > 0 {
		cxglTexImage2D(
			target,
			int32(level),
			internalFormat,
			width,
			height,
			0,
			inputFormat,
			inputType,
			data)
	}
}

// gogl
func opGlNewTexture(inputs []CXValue, outputs []CXValue) {
	var texture uint32
	cxglEnable(cxglTEXTURE_2D)
	cxglGenTextures(1, &texture)
	cxglBindTexture(cxglTEXTURE_2D, texture)
	cxglTexParameteri(cxglTEXTURE_2D, cxglTEXTURE_MIN_FILTER, cxglNEAREST)
	cxglTexParameteri(cxglTEXTURE_2D, cxglTEXTURE_MAG_FILTER, cxglNEAREST)
	cxglTexParameteri(cxglTEXTURE_2D, cxglTEXTURE_WRAP_S, cxglCLAMP_TO_EDGE)
	cxglTexParameteri(cxglTEXTURE_2D, cxglTEXTURE_WRAP_T, cxglCLAMP_TO_EDGE)

	uploadTexture(inputs[0].Get_str(), cxglTEXTURE_2D, 0, false)

	outputs[0].Set_i32(int32(texture))
}

func opGlNewTextureCube(inputs []CXValue, outputs []CXValue) {
	var texture uint32
	cxglEnable(cxglTEXTURE_CUBE_MAP)
	cxglGenTextures(1, &texture)
	cxglBindTexture(cxglTEXTURE_CUBE_MAP, texture)
	cxglTexParameteri(cxglTEXTURE_CUBE_MAP, cxglTEXTURE_MIN_FILTER, cxglNEAREST)
	cxglTexParameteri(cxglTEXTURE_CUBE_MAP, cxglTEXTURE_MAG_FILTER, cxglNEAREST)
	cxglTexParameteri(cxglTEXTURE_CUBE_MAP, cxglTEXTURE_WRAP_S, cxglCLAMP_TO_EDGE)
	cxglTexParameteri(cxglTEXTURE_CUBE_MAP, cxglTEXTURE_WRAP_T, cxglCLAMP_TO_EDGE)
	cxglTexParameteri(cxglTEXTURE_CUBE_MAP, cxglTEXTURE_WRAP_R, cxglCLAMP_TO_EDGE)

	var faces []string = []string{"posx", "negx", "posy", "negy", "posz", "negz"}
	var pattern string = inputs[0].Get_str()
	var extension string = inputs[1].Get_str()
	for i := 0; i < 6; i++ {
		uploadTexture(fmt.Sprintf("%s%s%s", pattern, faces[i], extension), uint32(cxglTEXTURE_CUBE_MAP_POSITIVE_X+i), 0, false)
	}
	outputs[0].Set_i32(int32(texture))
}

func opCxReleaseTexture(inputs []CXValue, outputs []CXValue) {
	textures[inputs[0].Get_str()] = Texture{}
}

func opCxTextureGetPixel(inputs []CXValue, outputs []CXValue) {
	var r float32
	var g float32
	var b float32
	var a float32

	var x = inputs[1].Get_i32()
	var y = inputs[2].Get_i32()

	if texture, ok := textures[inputs[0].Get_str()]; ok {
		var yoffset = y * texture.width * 4
		var xoffset = yoffset + x*4
		pixels := texture.pixels
		r = pixels[xoffset]
		g = pixels[xoffset+1]
		b = pixels[xoffset+2]
		a = pixels[xoffset+3]
	}
	outputs[0].Set_f32(r)
	outputs[1].Set_f32(g)
	outputs[2].Set_f32(b)
	outputs[3].Set_f32(a)
}

func opGlUploadImageToTexture(inputs []CXValue, outputs []CXValue) {
	uploadTexture(inputs[0].Get_str(), uint32(inputs[1].Get_i32()), uint32(inputs[2].Get_i32()), inputs[3].Get_bool())
}

func opGlNewGIF(inputs []CXValue, outputs []CXValue) {
	path := inputs[0].Get_str()

	file, err := CXOpenFile(path)
	defer file.Close()
	if err != nil {
		panic(fmt.Sprintf("file not found %q, %v", path, err))
	}

	reader := bufio.NewReader(file)
	gif, err := gif.DecodeAll(reader)
	if err != nil {
		panic(fmt.Sprintf("failed to decode file %q, %v", path, err))
	}

	gifs[path] = gif

	outputs[0].Set_i32(int32(len(gif.Image)))
	outputs[1].Set_i32(int32(gif.LoopCount))
	outputs[2].Set_i32(int32(gif.Config.Width))
	outputs[3].Set_i32(int32(gif.Config.Height))
}

func opGlFreeGIF(inputs []CXValue, outputs []CXValue) {
	gifs[inputs[0].Get_str()] = nil
}

func opGlGIFFrameToTexture(inputs []CXValue, outputs []CXValue) {
	path := inputs[0].Get_str()
	frame := inputs[1].Get_i32()
	texture := inputs[2].Get_i32()

	gif := gifs[path]
	img := gif.Image[frame]
	delay := int32(gif.Delay[frame])
	disposal := int32(gif.Disposal[frame])

	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{0, 0}, draw.Src)

	cxglBindTexture(cxglTEXTURE_2D, uint32(texture))
	cxglTexImage2D(
		cxglTEXTURE_2D,
		0,
		cxglRGBA,
		int32(rgba.Rect.Size().X),
		int32(rgba.Rect.Size().Y),
		0,
		cxglRGBA,
		cxglUNSIGNED_BYTE,
		rgba.Pix)

	outputs[0].Set_i32(delay)
	outputs[1].Set_i32(disposal)
}

func opGlAppend(inputs []CXValue, outputs []CXValue) {
	outputSlicePointer := outputs[0].Offset
	outputSliceOffset := GetPointerOffset(int32(outputSlicePointer))

    inputs[0].Used = int8(inputs[0].Type)

    inputSliceOffset := GetSliceOffset(inputs[0].FramePointer, inputs[0].Arg)
	var inputSliceLen int32
	if inputSliceOffset != 0 {
		inputSliceLen = GetSliceLen(inputSliceOffset)
	}

	obj := inputs[1].Get_bytes()

	objLen := int32(len(obj))
	outputSliceOffset = int32(SliceResizeEx(outputSliceOffset, inputSliceLen+objLen, 1))
	SliceCopyEx(outputSliceOffset, inputSliceOffset, inputSliceLen+objLen, 1)
	SliceAppendWriteByte(outputSliceOffset, obj, inputSliceLen)
	outputs[0].SetSlice(outputSliceOffset)
}

// gl_1_0
func opGlCullFace(inputs []CXValue, outputs []CXValue) {
	cxglCullFace(uint32(inputs[0].Get_i32()))
}

func opGlFrontFace(inputs []CXValue, outputs []CXValue) {
	cxglFrontFace(uint32(inputs[0].Get_i32()))
}

func opGlHint(inputs []CXValue, outputs []CXValue) {
	cxglHint(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlScissor(inputs []CXValue, outputs []CXValue) {
	cxglScissor(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_i32(),
		inputs[3].Get_i32())
}

func opGlTexParameteri(inputs []CXValue, outputs []CXValue) {
	cxglTexParameteri(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		inputs[2].Get_i32())
}

func opGlTexImage2D(inputs []CXValue, outputs []CXValue) {
	cxglTexImage2D(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		inputs[2].Get_i32(),
		inputs[3].Get_i32(),
		inputs[4].Get_i32(),
		inputs[5].Get_i32(),
		uint32(inputs[6].Get_i32()),
		uint32(inputs[7].Get_i32()),
        inputs[8].GetSlice())
}

func opGlClear(inputs []CXValue, outputs []CXValue) {
	cxglClear(uint32(inputs[0].Get_i32()))
}

func opGlClearColor(inputs []CXValue, outputs []CXValue) {
	cxglClearColor(
		inputs[0].Get_f32(),
		inputs[1].Get_f32(),
		inputs[2].Get_f32(),
		inputs[3].Get_f32())
}

func opGlClearStencil(inputs []CXValue, outputs []CXValue) {
	cxglClearStencil(inputs[0].Get_i32())
}

func opGlClearDepth(inputs []CXValue, outputs []CXValue) {
	cxglClearDepth(inputs[0].Get_f64())
}

func opGlStencilMask(inputs []CXValue, outputs []CXValue) {
	cxglStencilMask(uint32(inputs[0].Get_i32()))
}

func opGlColorMask(inputs []CXValue, outputs []CXValue) {
	cxglColorMask(
		inputs[0].Get_bool(),
		inputs[1].Get_bool(),
		inputs[2].Get_bool(),
		inputs[3].Get_bool())
}

func opGlDepthMask(inputs []CXValue, outputs []CXValue) {
	cxglDepthMask(inputs[0].Get_bool())
}

func opGlDisable(inputs []CXValue, outputs []CXValue) {
	cxglDisable(uint32(inputs[0].Get_i32()))
}

func opGlEnable(inputs []CXValue, outputs []CXValue) {
	cxglEnable(uint32(inputs[0].Get_i32()))
}

func opGlBlendFunc(inputs []CXValue, outputs []CXValue) {
	cxglBlendFunc(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlStencilFunc(inputs []CXValue, outputs []CXValue) {
	cxglStencilFunc(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		uint32(inputs[2].Get_i32()))
}

func opGlStencilOp(inputs []CXValue, outputs []CXValue) {
	cxglStencilOp(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		uint32(inputs[2].Get_i32()))
}

func opGlDepthFunc(inputs []CXValue, outputs []CXValue) {
	cxglDepthFunc(uint32(inputs[0].Get_i32()))
}

func opGlGetError(inputs []CXValue, outputs []CXValue) {
	outputs[0].Set_i32(int32(cxglGetError()))
}

func opGlGetTexLevelParameteriv(inputs []CXValue, outputs []CXValue) {
	var outValue int32 = 0
	cxglGetTexLevelParameteriv(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		uint32(inputs[2].Get_i32()),
		&outValue)
	outputs[0].Set_i32(outValue)
}

func opGlDepthRange(inputs []CXValue, outputs []CXValue) {
	cxglDepthRange(
		inputs[0].Get_f64(),
		inputs[1].Get_f64())
}

func opGlViewport(inputs []CXValue, outputs []CXValue) {
	cxglViewport(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_i32(),
		inputs[3].Get_i32())
}

// gl_1_1
func opGlDrawArrays(inputs []CXValue, outputs []CXValue) {
	cxglDrawArrays(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		inputs[2].Get_i32())
}

func opGlDrawElements(inputs []CXValue, outputs []CXValue) {
	cxglDrawElements(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		uint32(inputs[2].Get_i32()),
		inputs[3].GetSlice())
}

func opGlBindTexture(inputs []CXValue, outputs []CXValue) {
	cxglBindTexture(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlDeleteTextures(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglDeleteTextures(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
}

func opGlGenTextures(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglGenTextures(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
	outputs[0].Set_i32(int32(inpV1))
}

// gl_1_3
func opGlActiveTexture(inputs []CXValue, outputs []CXValue) {
	cxglActiveTexture(uint32(inputs[0].Get_i32()))
}

// gl_1_4
func opGlBlendFuncSeparate(inputs []CXValue, outputs []CXValue) {
	cxglBlendFuncSeparate(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		uint32(inputs[2].Get_i32()),
		uint32(inputs[3].Get_i32()))
}

// gl_1_5
func opGlBindBuffer(inputs []CXValue, outputs []CXValue) {
	cxglBindBuffer(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlDeleteBuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglDeleteBuffers(
		inputs[0].Get_i32(),
		&inpV1) // will panic if inp0 > 1
}

func opGlGenBuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglGenBuffers(
		inputs[0].Get_i32(),
		&inpV1) // will panic if inp0 > 1
	outputs[0].Set_i32(int32(inpV1))
}

func opGlBufferData(inputs []CXValue, outputs []CXValue) {
	cxglBufferData(
		uint32(inputs[0].Get_i32()),
		int(inputs[1].Get_i32()),
		inputs[2].GetSlice_ui8(),
		uint32(inputs[3].Get_i32()))
}

func opGlBufferSubData(inputs []CXValue, outputs []CXValue) {
	cxglBufferSubData(
		uint32(inputs[0].Get_i32()),
		int(inputs[1].Get_i32()),
		int(inputs[2].Get_i32()),
		inputs[3].GetSlice())
}

func opGlDrawBuffers(inputs []CXValue, outputs []CXValue) {
	cxglDrawBuffers(
		inputs[0].Get_i32(),
		inputs[1].GetSlice_ui32())
}

func opGlStencilOpSeparate(inputs []CXValue, outputs []CXValue) {
	cxglStencilOpSeparate(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		uint32(inputs[2].Get_i32()),
		uint32(inputs[3].Get_i32()))
}

func opGlStencilFuncSeparate(inputs []CXValue, outputs []CXValue) {
	cxglStencilFuncSeparate(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		inputs[2].Get_i32(),
		uint32(inputs[3].Get_i32()))
}

func opGlStencilMaskSeparate(inputs []CXValue, outputs []CXValue) {
	cxglStencilMaskSeparate(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlAttachShader(inputs []CXValue, outputs []CXValue) {
	cxglAttachShader(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlBindAttribLocation(inputs []CXValue, outputs []CXValue) {
	cxglBindAttribLocation(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		inputs[2].Get_str())
}

func opGlCompileShader(inputs []CXValue, outputs []CXValue) {
	shader := uint32(inputs[0].Get_i32())
	cxglCompileShader(shader)
}

func opGlCreateProgram(inputs []CXValue, outputs []CXValue) {
	outputs[0].Set_i32(int32(cxglCreateProgram()))
}

func opGlCreateShader(inputs []CXValue, outputs []CXValue) {
	outV0 := int32(cxglCreateShader(uint32(inputs[0].Get_i32())))
	outputs[0].Set_i32(outV0)
}

func opGlDeleteProgram(inputs []CXValue, outputs []CXValue) {
	cxglDeleteShader(uint32(inputs[0].Get_i32()))
}

func opGlDeleteShader(inputs []CXValue, outputs []CXValue) {
	cxglDeleteShader(uint32(inputs[0].Get_i32()))
}

func opGlDetachShader(inputs []CXValue, outputs []CXValue) {
	cxglDetachShader(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlEnableVertexAttribArray(inputs []CXValue, outputs []CXValue) {
	cxglEnableVertexAttribArray(uint32(inputs[0].Get_i32()))
}

func opGlGetAttribLocation(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetAttribLocation(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_str())
	outputs[0].Set_i32(outV0)
}

func opGlGetProgramiv(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetProgramiv(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
	outputs[0].Set_i32(outV0)
}

func opGlGetProgramInfoLog(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetProgramInfoLog(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32())
	outputs[0].Set_str(outV0)
}

func opGlGetShaderiv(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetShaderiv(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
	outputs[0].Set_i32(outV0)
}

func opGlGetShaderInfoLog(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetShaderInfoLog(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32())
	outputs[0].Set_str(outV0)
}

func opGlGetUniformLocation(inputs []CXValue, outputs []CXValue) {
	outV0 := cxglGetUniformLocation(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_str())
	outputs[0].Set_i32(outV0)
}

func opGlLinkProgram(inputs []CXValue, outputs []CXValue) {
	program := uint32(inputs[0].Get_i32())
	cxglLinkProgram(program)
}

func opGlShaderSource(inputs []CXValue, outputs []CXValue) {
	cxglShaderSource(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		inputs[2].Get_str())
}

func opGlUseProgram(inputs []CXValue, outputs []CXValue) {
	cxglUseProgram(uint32(inputs[0].Get_i32()))
}

func opGlUniform1f(inputs []CXValue, outputs []CXValue) {
	cxglUniform1f(
		inputs[0].Get_i32(),
		inputs[1].Get_f32())
}

func opGlUniform2f(inputs []CXValue, outputs []CXValue) {
	cxglUniform2f(
		inputs[0].Get_i32(),
		inputs[1].Get_f32(),
		inputs[2].Get_f32())
}

func opGlUniform3f(inputs []CXValue, outputs []CXValue) {
	cxglUniform3f(
		inputs[0].Get_i32(),
		inputs[1].Get_f32(),
		inputs[2].Get_f32(),
		inputs[3].Get_f32())
}

func opGlUniform4f(inputs []CXValue, outputs []CXValue) {
	cxglUniform4f(
		inputs[0].Get_i32(),
		inputs[1].Get_f32(),
		inputs[2].Get_f32(),
		inputs[3].Get_f32(),
		inputs[4].Get_f32())
}

func opGlUniform1i(inputs []CXValue, outputs []CXValue) {
	cxglUniform1i(
		inputs[0].Get_i32(),
		inputs[1].Get_i32())
}

func opGlUniform2i(inputs []CXValue, outputs []CXValue) {
	cxglUniform2i(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_i32())
}

func opGlUniform3i(inputs []CXValue, outputs []CXValue) {
	cxglUniform3i(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_i32(),
		inputs[3].Get_i32())
}

func opGlUniform4i(inputs []CXValue, outputs []CXValue) {
	cxglUniform4i(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_i32(),
		inputs[3].Get_i32(),
		inputs[4].Get_i32())
}

func opGlUniform1fv(inputs []CXValue, outputs []CXValue) {
	cxglUniform1fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_f32())
}

func opGlUniform2fv(inputs []CXValue, outputs []CXValue) {
	cxglUniform2fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_f32())
}

func opGlUniform3fv(inputs []CXValue, outputs []CXValue) {
	cxglUniform3fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_f32())
}

func opGlUniform4fv(inputs []CXValue, outputs []CXValue) {
	cxglUniform4fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_f32())
}

func opGlUniform1iv(inputs []CXValue, outputs []CXValue) {
	cxglUniform1iv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_i32())
}

func opGlUniform2iv(inputs []CXValue, outputs []CXValue) {
	cxglUniform2iv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_i32())
}

func opGlUniform3iv(inputs []CXValue, outputs []CXValue) {
	cxglUniform3iv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_i32())
}

func opGlUniform4iv(inputs []CXValue, outputs []CXValue) {
	cxglUniform4iv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].GetSlice_i32())
}

func opGlUniformMatrix2fv(inputs []CXValue, outputs []CXValue) {
	cxglUniformMatrix2fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_bool(),
		inputs[3].GetSlice_f32())
}

func opGlUniformMatrix3fv(inputs []CXValue, outputs []CXValue) {
	cxglUniformMatrix3fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_bool(),
		inputs[3].GetSlice_f32())
}

func opGlUniformMatrix4fv(inputs []CXValue, outputs []CXValue) {
	cxglUniformMatrix4fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_bool(),
		inputs[3].GetSlice_f32())
}

func opGlUniformV4F(inputs []CXValue, outputs []CXValue) {
	cxglUniform4fv(
		inputs[0].Get_i32(),
		1,
		inputs[1].GetSlice())
}

func opGlUniformM44F(inputs []CXValue, outputs []CXValue) {
	cxglUniformMatrix4fv(
		inputs[0].Get_i32(),
		1,
		inputs[1].Get_bool(),
		inputs[2].GetSlice())
}

func opGlUniformM44FV(inputs []CXValue, outputs []CXValue) {
	cxglUniformMatrix4fv(
		inputs[0].Get_i32(),
		inputs[1].Get_i32(),
		inputs[2].Get_bool(),
		inputs[3].GetSlice())
}

func opGlVertexAttribPointer(inputs []CXValue, outputs []CXValue) {
	cxglVertexAttribPointer(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		uint32(inputs[2].Get_i32()),
		inputs[3].Get_bool(),
		inputs[4].Get_i32(), 0)
}

func opGlVertexAttribPointerI32(inputs []CXValue, outputs []CXValue) {
	cxglVertexAttribPointer(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		uint32(inputs[2].Get_i32()),
		inputs[3].Get_bool(),
		inputs[4].Get_i32(),
		inputs[5].Get_i32())
}

func opGlClearBufferI(inputs []CXValue, outputs []CXValue) {
	color := []int32{
		inputs[2].Get_i32(),
		inputs[3].Get_i32(),
		inputs[4].Get_i32(),
		inputs[5].Get_i32()}

	cxglClearBufferiv(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		color)
}

func opGlClearBufferUI(inputs []CXValue, outputs []CXValue) {
	color := []uint32{
		inputs[2].Get_ui32(),
		inputs[3].Get_ui32(),
		inputs[4].Get_ui32(),
		inputs[5].Get_ui32()}

	cxglClearBufferuiv(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		color)
}

func opGlClearBufferF(inputs []CXValue, outputs []CXValue) {
	color := []float32{
		inputs[2].Get_f32(),
		inputs[3].Get_f32(),
		inputs[4].Get_f32(),
		inputs[5].Get_f32()}

	cxglClearBufferfv(
		uint32(inputs[0].Get_i32()),
		inputs[1].Get_i32(),
		color)
}

func opGlBindRenderbuffer(inputs []CXValue, outputs []CXValue) {
	cxglBindRenderbuffer(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlDeleteRenderbuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglDeleteRenderbuffers(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
}

func opGlGenRenderbuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglGenRenderbuffers(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
	outputs[0].Set_i32(int32(inpV1))
}

func opGlRenderbufferStorage(inputs []CXValue, outputs []CXValue) {
	cxglRenderbufferStorage(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		inputs[2].Get_i32(),
		inputs[3].Get_i32())
}

func opGlBindFramebuffer(inputs []CXValue, outputs []CXValue) {
	cxglBindFramebuffer(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()))
}

func opGlDeleteFramebuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglDeleteFramebuffers(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
}

func opGlGenFramebuffers(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglGenFramebuffers(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
	outputs[0].Set_i32(int32(inpV1))
}

func opGlCheckFramebufferStatus(inputs []CXValue, outputs []CXValue) {
	outV0 := int32(cxglCheckFramebufferStatus(uint32(inputs[0].Get_i32())))
	outputs[0].Set_i32(outV0)
}

func opGlFramebufferTexture2D(inputs []CXValue, outputs []CXValue) {
	cxglFramebufferTexture2D(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		uint32(inputs[2].Get_i32()),
		uint32(inputs[3].Get_i32()),
		inputs[4].Get_i32())
}

func opGlFramebufferRenderbuffer(inputs []CXValue, outputs []CXValue) {
	cxglFramebufferRenderbuffer(
		uint32(inputs[0].Get_i32()),
		uint32(inputs[1].Get_i32()),
		uint32(inputs[2].Get_i32()),
		uint32(inputs[3].Get_i32()))
}

func opGlGenerateMipmap(inputs []CXValue, outputs []CXValue) {
	cxglGenerateMipmap(uint32(inputs[0].Get_i32()))
}

func opGlBindVertexArray(inputs []CXValue, outputs []CXValue) {
	inpV0 := uint32(inputs[0].Get_i32())
	if runtime.GOOS == "darwin" {
		cxglBindVertexArrayAPPLE(inpV0)
	} else {
		cxglBindVertexArray(inpV0)
	}
}

func opGlBindVertexArrayCore(inputs []CXValue, outputs []CXValue) {
	cxglBindVertexArray(uint32(inputs[0].Get_i32()))
}

func opGlDeleteVertexArrays(inputs []CXValue, outputs []CXValue) {
	inpV0 := inputs[0].Get_i32()
	inpV1 := uint32(inputs[1].Get_i32())
	if runtime.GOOS == "darwin" {
		cxglDeleteVertexArraysAPPLE(inpV0, &inpV1) // will panic if inp0 > 1
	} else {
		cxglDeleteVertexArrays(inpV0, &inpV1) // will panic if inp0 > 1
	}
}

func opGlDeleteVertexArraysCore(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglDeleteVertexArrays(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
}

func opGlGenVertexArrays(inputs []CXValue, outputs []CXValue) {
	inpV0 := inputs[0].Get_i32()
	inpV1 := uint32(inputs[1].Get_i32())
	if runtime.GOOS == "darwin" {
		cxglGenVertexArraysAPPLE(inpV0, &inpV1) // will panic if inp0 > 1
	} else {
		cxglGenVertexArrays(inpV0, &inpV1) // will panic if inp0 > 1
	}
	outputs[0].Set_i32(int32(inpV1))
}

func opGlGenVertexArraysCore(inputs []CXValue, outputs []CXValue) {
	inpV1 := uint32(inputs[1].Get_i32())
	cxglGenVertexArrays(inputs[0].Get_i32(), &inpV1) // will panic if inp0 > 1
	outputs[0].Set_i32(int32(inpV1))
}
