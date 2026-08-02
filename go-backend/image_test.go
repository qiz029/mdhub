package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gen2brain/webp"
)

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 0x44, G: 0x88, B: 0xcc, A: 0xff})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDetectUploadImageMIME(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...), "image/png"},
		{"jpeg", []byte("\xff\xd8\xff\xe0jpeg"), "image/jpeg"},
		{"gif", []byte("GIF89aimage"), "image/gif"},
		{"webp", []byte("RIFF\x10\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"avif", []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avif"), "image/avif"},
		{"svg rejected", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), ""},
		{"text rejected", []byte("not an image"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectUploadImageMIME(tt.data); got != tt.want {
				t.Fatalf("detectUploadImageMIME() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseImageUpload(t *testing.T) {
	image := testPNG(t, 16, 16)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest("POST", "/api/images", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	upload, code, err := parseImageUpload(httptest.NewRecorder(), request)
	if err != nil {
		t.Fatal(err)
	}
	if code != 200 || upload.mime != "image/png" || !bytes.Equal(upload.data, image) {
		t.Fatalf("upload = %+v, code = %d", upload, code)
	}
}

func TestOptimizeUploadedImageResizesAndEncodesWebP(t *testing.T) {
	data := testPNG(t, 2601, 4)
	optimized, mime, err := optimizeUploadedImage(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/webp" {
		t.Fatalf("mime = %q, want image/webp", mime)
	}
	config, err := webp.DecodeConfig(bytes.NewReader(optimized))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 2560 || config.Height != 3 {
		t.Fatalf("dimensions = %dx%d, want 2560x3", config.Width, config.Height)
	}
}

func TestOptimizeUploadedImageCompressesLargeStaticImage(t *testing.T) {
	data := append(testPNG(t, 16, 16), make([]byte, optimizeAboveBytes)...)
	optimized, mime, err := optimizeUploadedImage(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/webp" || len(optimized) >= len(data) {
		t.Fatalf("mime = %q, size = %d; want smaller WebP", mime, len(optimized))
	}
}

func TestIsAnimatedWebP(t *testing.T) {
	animated := []byte("RIFF\x0c\x00\x00\x00WEBPANIM\x00\x00\x00\x00")
	static := []byte("RIFF\x0c\x00\x00\x00WEBPVP8 \x00\x00\x00\x00")
	if !isAnimatedWebP(animated) {
		t.Fatal("ANIM chunk was not recognized")
	}
	if isAnimatedWebP(static) {
		t.Fatal("static WebP was recognized as animated")
	}
}

func TestOptimizeUploadedImageRejectsInvalidAVIF(t *testing.T) {
	data := []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avif")
	if _, _, err := optimizeUploadedImage(data, "image/avif"); err == nil {
		t.Fatal("invalid AVIF was accepted")
	}
}

func TestOptimizeUploadedImageRejectsHugeGIFDimensions(t *testing.T) {
	data := []byte("GIF89a\x88\x13\x88\x13\x00\x00\x00") // 5000 x 5000
	if _, _, err := optimizeUploadedImage(data, "image/gif"); err == nil {
		t.Fatal("25 megapixel GIF was accepted")
	}
}

func TestParseImageUploadRejectsOversizedFile(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxImageUploadBytes)...)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "too-large.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest("POST", "/api/images", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	_, code, err := parseImageUpload(httptest.NewRecorder(), request)
	if err == nil || code != 413 {
		t.Fatalf("code = %d, err = %v; want 413", code, err)
	}
}

func TestUploadedImagePathIsContentAddressed(t *testing.T) {
	first := uploadedImagePath([]byte("same image"), "image/webp")
	second := uploadedImagePath([]byte("same image"), "image/webp")
	different := uploadedImagePath([]byte("different image"), "image/webp")

	if first != second {
		t.Fatalf("same content paths differ: %q != %q", first, second)
	}
	if first == different {
		t.Fatalf("different content paths are equal: %q", first)
	}
	if !strings.HasPrefix(first, "uploads/") || !strings.HasSuffix(first, ".webp") {
		t.Fatalf("path = %q, want uploads/...webp", first)
	}
	if !isContentAddressedUploadPath(first, []byte("same image"), "image/webp") {
		t.Fatalf("generated path %q was not recognized as content-addressed", first)
	}
	if isContentAddressedUploadPath("uploads/imported.png", []byte("same image"), "image/png") {
		t.Fatal("imported mutable path was recognized as content-addressed")
	}
	if isContentAddressedUploadPath(first, []byte("changed image"), "image/webp") {
		t.Fatal("path whose content no longer matches its hash was recognized as immutable")
	}
}

func TestReservedUploadPath(t *testing.T) {
	if !isReservedUploadPath("uploads/ab/hash.webp") {
		t.Fatal("content-addressed upload namespace was not reserved")
	}
	if isReservedUploadPath("notes/uploads/example.png") {
		t.Fatal("nested ordinary asset path was incorrectly reserved")
	}
}

type endlessZeroReader struct{}

func (endlessZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestPutDocumentRejectsOversizedBody(t *testing.T) {
	body := io.LimitReader(endlessZeroReader{}, maxDocumentBytes+1)
	request := httptest.NewRequest(http.MethodPut, "/api/documents/test", body)
	response := httptest.NewRecorder()
	putDocument(response, request, "test")
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
}

func TestGetImageSetsSandboxedContentHeaders(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT data, mime FROM images").
		WithArgs("vault/diagram.svg").
		WillReturnRows(sqlmock.NewRows([]string{"data", "mime"}).AddRow([]byte("<svg/>"), "image/svg+xml"))

	request := httptest.NewRequest(http.MethodGet, "/api/images?path=vault/diagram.svg", nil)
	response := httptest.NewRecorder()
	getImage(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Fatalf("CSP = %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
