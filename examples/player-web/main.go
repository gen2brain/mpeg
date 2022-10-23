package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gowebapi/webapi"
	"github.com/gowebapi/webapi/core/js"
	"github.com/gowebapi/webapi/core/jsconv"
	"github.com/gowebapi/webapi/dom"
	"github.com/gowebapi/webapi/graphics/webgl"
	"github.com/gowebapi/webapi/html"
	"github.com/gowebapi/webapi/html/canvas"
	"github.com/gowebapi/webapi/html/htmlcommon"
	"github.com/gowebapi/webapi/html/htmlevent"
	"github.com/gowebapi/webapi/media/audio"
	"github.com/jfbus/httprs"

	"github.com/gen2brain/mpeg"
)

type app struct {
	width  int
	height int

	lastTime    float64
	elapsedTime float64
	nextPos     float64

	framerate  float64
	samplerate float32

	pause       bool
	animationID uint

	mpg    *mpeg.MPEG
	reader io.ReadSeekCloser

	window *webapi.Window
	canvas *canvas.HTMLCanvasElement
	status *dom.Element

	audioContext *audio.AudioContext
	audioBuffer  *audio.AudioBuffer

	gl       *webgl.RenderingContext
	callback *htmlcommon.FrameRequestCallback

	program      *webgl.Program
	vertexBuffer *webgl.Buffer

	textureY  *webgl.Texture
	textureCb *webgl.Texture
	textureCr *webgl.Texture
}

func newApp() *app {
	app := &app{}
	app.window = webapi.GetWindow()

	fName := "sintel.mpg"
	res, err := http.Get(app.window.Location().Origin() + "/" + fName)
	if err != nil {
		println(err.Error())
	}
	app.reader = httprs.NewHttpReadSeeker(res)

	app.mpg, err = mpeg.New(app.reader)
	if err != nil {
		println(err.Error())
	}

	app.width = app.mpg.Width()
	app.height = app.mpg.Height()
	app.framerate = app.mpg.Framerate()
	app.samplerate = float32(app.mpg.Samplerate())

	canvasE := app.window.Document().GetElementById("canvas")
	app.canvas = canvas.HTMLCanvasElementFromWrapper(canvasE)
	app.canvas.SetWidth(uint(app.width))
	app.canvas.SetHeight(uint(app.height))

	text := "click on canvas to play video"
	app.status = app.window.Document().GetElementById("status")
	app.status.SetInnerHTML(text)

	contextU := app.canvas.GetContext("webgl", map[string]interface{}{
		"alpha":                 false,
		"depth":                 false,
		"stencil":               false,
		"antialias":             false,
		"premultipliedAlpha":    false,
		"preserveDrawingBuffer": false,
	})
	gl := webgl.RenderingContextFromWrapper(contextU)

	app.gl = gl
	app.callback = htmlcommon.FrameRequestCallbackToJS(app.update)

	gl.PixelStorei(webgl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, 0)

	app.vertexBuffer = gl.CreateBuffer()
	vertexCoords := []float32{0, 0, 0, 1, 1, 0, 1, 1}
	gl.BindBuffer(webgl.ARRAY_BUFFER, app.vertexBuffer)
	gl.BufferData2(webgl.ARRAY_BUFFER, webgl.UnionFromJS(jsconv.Float32ToJs(vertexCoords)), webgl.STATIC_DRAW)

	app.program = app.createProgram(vertexShader, fragmentShader)
	vertexAttr := gl.GetAttribLocation(app.program, "vertex")
	gl.EnableVertexAttribArray(uint(vertexAttr))
	gl.VertexAttribPointer(uint(vertexAttr), 2, webgl.FLOAT, false, 0, 0)

	app.textureY = app.createTexture(0, "textureY")
	app.textureCb = app.createTexture(1, "textureCb")
	app.textureCr = app.createTexture(2, "textureCr")

	app.mpg.SetVideoCallback(func(m *mpeg.MPEG, frame *mpeg.Frame) {
		if frame == nil {
			return
		}

		app.render(frame.Y.Data, frame.Cb.Data, frame.Cr.Data)
	})

	app.mpg.SetAudioCallback(func(m *mpeg.MPEG, samples *mpeg.Samples) {
		if samples == nil {
			return
		}

		app.audioBuffer = app.audioContext.CreateBuffer(2, mpeg.SamplesPerFrame, app.samplerate)
		if !app.audioBuffer.JSValue().Get("copyToChannel").IsUndefined() {
			app.audioBuffer.JSValue().Call("copyToChannel", jsconv.Float32ToJs(samples.Left), 0)
			app.audioBuffer.JSValue().Call("copyToChannel", jsconv.Float32ToJs(samples.Right), 1)
		} else {
			app.audioBuffer.GetChannelData(0).JSValue().Call("set", jsconv.Float32ToJs(samples.Left))
			app.audioBuffer.GetChannelData(1).JSValue().Call("set", jsconv.Float32ToJs(samples.Right))
		}

		ct := app.audioContext.CurrentTime()
		if app.nextPos < ct {
			app.nextPos = ct
		}

		audioSource := app.audioContext.CreateBufferSource()
		audioSource.SetBuffer(app.audioBuffer)
		audioSource.JSValue().Call("connect", app.audioContext.Destination().JSValue())
		audioSource.JSValue().Call("start", app.nextPos)

		app.nextPos += app.audioBuffer.Duration()
	})

	status := fmt.Sprintf("%s - %dx%d %.2ffps / %dch %dHz",
		fName, app.mpg.Width(), app.mpg.Height(), app.mpg.Framerate(), app.mpg.Channels(), app.mpg.Samplerate())

	app.canvas.SetOnClick(func(event *htmlevent.MouseEvent, currentTarget *html.HTMLElement) {
		if app.audioContext == nil {
			app.audioContext = audio.NewAudioContext(&audio.AudioContextOptions{
				SampleRate:  app.samplerate,
				LatencyHint: audio.UnionFromJS(js.ValueOf("playback")),
			})
			app.audioContext.Resume()

			app.audioBuffer = app.audioContext.CreateBuffer(2, mpeg.SamplesPerFrame, app.samplerate)
			app.mpg.SetAudioLeadTime(app.audioBuffer.Duration())

			app.animationID = app.window.RequestAnimationFrame(app.callback)
			app.status.SetInnerHTML(status)
			return
		}

		if app.pause {
			app.audioContext.Resume()
			app.status.SetInnerHTML(status)
			app.animationID = app.window.RequestAnimationFrame(app.callback)
		} else {
			app.audioContext.Suspend()
			app.status.SetInnerHTML(text)
		}

		app.pause = !app.pause
	})

	return app
}

func (a *app) update(currentTime float64) {
	if a.pause {
		return
	}

	currentTime = currentTime / 1000
	a.elapsedTime = currentTime - a.lastTime
	if a.elapsedTime > 1.0/a.framerate {
		a.elapsedTime = 1.0 / a.framerate
	}
	a.lastTime = currentTime

	go func() {
		a.mpg.Decode(a.elapsedTime)
		a.animationID = a.window.RequestAnimationFrame(a.callback)
	}()
}

func (a *app) destroy() {
	gl := a.gl
	a.window.CancelAnimationFrame(a.animationID)

	a.deleteTexture(webgl.TEXTURE0, a.textureY)
	a.deleteTexture(webgl.TEXTURE1, a.textureCb)
	a.deleteTexture(webgl.TEXTURE2, a.textureCr)

	gl.DeleteProgram(a.program)
	gl.DeleteBuffer(a.vertexBuffer)
	gl.GetExtension("WEBGL_lose_context").JSValue().Call("loseContext")

	a.audioContext.Close()
	a.canvas.Remove()
	a.status.Remove()

	err := a.reader.Close()
	if err != nil {
		println(err.Error())
	}
}

func (a *app) createTexture(index int, name string) *webgl.Texture {
	gl := a.gl
	texture := gl.CreateTexture()

	gl.BindTexture(webgl.TEXTURE_2D, texture)
	gl.TexParameteri(webgl.TEXTURE_2D, webgl.TEXTURE_MAG_FILTER, int(webgl.LINEAR))
	gl.TexParameteri(webgl.TEXTURE_2D, webgl.TEXTURE_MIN_FILTER, int(webgl.LINEAR))
	gl.TexParameteri(webgl.TEXTURE_2D, webgl.TEXTURE_WRAP_S, int(webgl.CLAMP_TO_EDGE))
	gl.TexParameteri(webgl.TEXTURE_2D, webgl.TEXTURE_WRAP_T, int(webgl.CLAMP_TO_EDGE))
	gl.Uniform1i(gl.GetUniformLocation(a.program, name), index)

	return texture
}

func (a *app) updateTexture(unit uint, texture *webgl.Texture, w, h int, data *webgl.Union) {
	gl := a.gl
	gl.ActiveTexture(unit)
	gl.BindTexture(webgl.TEXTURE_2D, texture)
	gl.TexImage2D(webgl.TEXTURE_2D, 0, int(webgl.LUMINANCE), w, h, 0, webgl.LUMINANCE, webgl.UNSIGNED_BYTE, data)
}

func (a *app) deleteTexture(unit uint, texture *webgl.Texture) {
	gl := a.gl
	gl.ActiveTexture(unit)
	gl.DeleteTexture(texture)
}

func (a *app) render(y, cb, cr []byte) {
	gl := a.gl

	w := ((a.width + 15) >> 4) << 4
	h := a.height
	w2 := w >> 1
	h2 := h >> 1

	gl.UseProgram(a.program)

	a.updateTexture(webgl.TEXTURE0, a.textureY, w, h, webgl.UnionFromJS(jsconv.UInt8ToJs(y)))
	a.updateTexture(webgl.TEXTURE1, a.textureCb, w2, h2, webgl.UnionFromJS(jsconv.UInt8ToJs(cb)))
	a.updateTexture(webgl.TEXTURE2, a.textureCr, w2, h2, webgl.UnionFromJS(jsconv.UInt8ToJs(cr)))

	gl.DrawArrays(webgl.TRIANGLE_STRIP, 0, 4)
}

func (a *app) createProgram(vsh, fsh string) *webgl.Program {
	gl := a.gl
	program := gl.CreateProgram()

	vs, err := a.compileShader(webgl.VERTEX_SHADER, vsh)
	if err != nil {
		println(err.Error())
	}

	fs, err := a.compileShader(webgl.FRAGMENT_SHADER, fsh)
	if err != nil {
		println(err.Error())
	}

	gl.AttachShader(program, vs)
	gl.AttachShader(program, fs)
	gl.LinkProgram(program)
	gl.UseProgram(program)

	return program
}

func (a *app) compileShader(typ uint, source string) (*webgl.Shader, error) {
	gl := a.gl
	shader := gl.CreateShader(typ)

	gl.ShaderSource(shader, source)
	gl.CompileShader(shader)

	if !gl.GetShaderParameter(shader, webgl.COMPILE_STATUS).Bool() {
		return nil, errors.New(*gl.GetShaderInfoLog(shader))
	}

	return shader, nil
}

func main() {
	app := newApp()
	defer app.destroy()

	<-app.mpg.Done()
}

const vertexShader = `
        attribute vec2 vertex;
        varying vec2 texCoord;

        void main() {
                texCoord = vertex;
                gl_Position = vec4((vertex * 2.0 - 1.0) * vec2(1, -1), 0.0, 1.0);
        }`

const fragmentShader = `
		precision mediump float;
        uniform sampler2D textureY;
        uniform sampler2D textureCb;
        uniform sampler2D textureCr;
        varying vec2 texCoord;
        
        mat4 rec601 = mat4(
                1.16438,  0.00000,  1.59603, -0.87079,
                1.16438, -0.39176, -0.81297,  0.52959,
                1.16438,  2.01723,  0.00000, -1.08139,
                0, 0, 0, 1
        );
        
        void main() {
                float y = texture2D(textureY, texCoord).r;
                float cb = texture2D(textureCb, texCoord).r;
                float cr = texture2D(textureCr, texCoord).r;
        
                gl_FragColor = vec4(y, cb, cr, 1.0) * rec601;
        }`
