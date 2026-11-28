package bff

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
)

func prototypeReviewFixture(t *testing.T) (*Server, *artifacts.Store, http.Handler, *fakeDapr) {
	t.Helper()
	h := newHarness(t)
	s := NewServer(h.st, nil, NewSessions([]byte("test-key")), "")
	repo := artifacts.New(t.TempDir())
	s.Pipe = &pipeline.Pipeline{Store: repo}
	registerUnit(t, s.Log, "fee")
	registerUnit(t, s.Log, "other")
	s.Log.Notify = s.NotifyChange
	return s, repo, s.Handler(), h.fake
}

func prototypeReviewRequest(handler http.Handler, method, path string, body io.Reader, contentType string, role Role, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if role != "" {
		cookie := httptest.NewRecorder()
		NewSessions([]byte("test-key")).Issue(cookie, role)
		req.AddCookie(cookie.Result().Cookies()[0])
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func uploadPrototypeFixture(t *testing.T, handler http.Handler, query, name string, data []byte, role Role) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := prototypeMultipart(t, name, data)
	return prototypeReviewRequest(handler, "POST", "/api/prototype-imports"+query, body, contentType, role, nil)
}

func prototypeMultipart(t *testing.T, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}

func prototypeReviewImage(t *testing.T, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := encode(&data, image.NewRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func reviewStatus(t *testing.T, resp *httptest.ResponseRecorder, want int) {
	t.Helper()
	if resp.Code != want {
		t.Fatalf("status = %d, want %d, body = %.200s", resp.Code, want, resp.Body.String())
	}
}

func TestPrototypeImportValidation(t *testing.T) {
	s, repo, handler, _ := prototypeReviewFixture(t)
	pngData := prototypeReviewImage(t, func(w *bytes.Buffer, img image.Image) error { return png.Encode(w, img) })
	// 只替换 IHDR；大图在解码像素前按尺寸拒绝。
	largePNG := append([]byte(nil), pngData...)
	binary.BigEndian.PutUint32(largePNG[16:20], 1<<16)
	binary.BigEndian.PutUint32(largePNG[29:33], crc32.ChecksumIEEE(largePNG[12:29]))
	jpegData := prototypeReviewImage(t, func(w *bytes.Buffer, img image.Image) error { return jpeg.Encode(w, img, nil) })
	for _, tt := range []struct {
		name, query, file string
		data              []byte
		role              Role
		status            int
	}{
		{"role", "?pbc=fee", "review.png", pngData, RoleDA, 403},
		{"no-session", "?pbc=fee", "review.png", pngData, "", 403},
		{"explicit-unit", "", "review.png", pngData, RolePA, 400},
		{"blank-unit", "?pbc=", "review.png", pngData, RolePA, 400},
		{"unknown-unit", "?pbc=missing", "review.png", pngData, RolePA, 404},
		{"size", "?pbc=fee", "huge.png", make([]byte, (20<<20)+1), RolePA, 413},
		{"wrong-format", "?pbc=fee", "review.png", []byte("plain text"), RolePA, 400},
		{"png-truncated", "?pbc=fee", "review.png", pngData[:len(pngData)-16], RolePA, 400},
		{"png-dimensions", "?pbc=fee", "review.png", largePNG, RolePA, 400},
		{"jpeg-truncated", "?pbc=fee", "review.jpg", jpegData[:len(jpegData)-12], RolePA, 400},
		{"pdf-truncated", "?pbc=fee", "review.pdf", []byte("%PDF-1.7\n1 0 obj\n"), RolePA, 400},
		{"pdf-bad-version", "?pbc=fee", "review.pdf", []byte("%PDF-xy!\n%%EOF\n"), RolePA, 400},
		{"svg", "?pbc=fee", "review.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>图标</title></svg>`), RolePA, 400},
		{"html-no-markup", "?pbc=fee", "review.html", []byte("ordinary text < 25"), RolePA, 400},
		{"html-encoding", "?pbc=fee", "review.html", []byte("<h1>\xff</h1>"), RolePA, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := uploadPrototypeFixture(t, handler, tt.query, tt.file, tt.data, tt.role)
			reviewStatus(t, response, tt.status)
			if tt.name == "wrong-format" && !strings.Contains(response.Body.String(), "原型文件格式不受支持") {
				t.Fatalf("unsupported format error: %s", response.Body.String())
			}
		})
	}
	facts, err := s.Log.Load(t.Context(), "fee")
	if err != nil || len(facts) != 0 {
		t.Fatalf("invalid request changed ledger: %v, %v", facts, err)
	}
	blobs, err := repo.List()
	if err != nil || len(blobs) != 0 {
		t.Fatalf("invalid request wrote blobs: %v, %v", blobs, err)
	}
}

func TestPrototypeImportReadOnlyReview(t *testing.T) {
	s, repo, handler, _ := prototypeReviewFixture(t)
	// 已落账的正式提交与老框选批注都要照常回放。
	seedCommit(t, s.Log, "fee")
	legacyBody := `{"x":0,"y":0.2,"w":0.3,"h":0.4,"text":"旧框选","round":"待提交"}`
	legacy := prototypeReviewRequest(handler, "POST", "/api/annotations", bytes.NewBufferString(legacyBody), "application/json", RolePA, nil)
	reviewStatus(t, legacy, 201)
	before := prototypeReviewRequest(handler, "GET", "/api/state?pbc=fee", &bytes.Buffer{}, "", RoleBA, nil)
	reviewStatus(t, before, 200)
	previousFacts, err := s.Log.Load(t.Context(), "fee")
	if err != nil {
		t.Fatal(err)
	}

	pngData := prototypeReviewImage(t, func(w *bytes.Buffer, img image.Image) error { return png.Encode(w, img) })
	jpegData := prototypeReviewImage(t, func(w *bytes.Buffer, img image.Image) error { return jpeg.Encode(w, img, nil) })
	files := []struct {
		name, mediaType string
		data            []byte
	}{
		{"原型.png", "image/png", pngData},
		{"same.png", "image/png", pngData},
		{"review.jpg", "image/jpeg", jpegData},
		{"review.pdf", "application/pdf", prototypePDFPage(false)},
		{"review.html", "text/html", []byte("<!doctype html><h1 style=\"color:green\">原型</h1>")},
	}
	frame := map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin"}
	imports := make([]map[string]any, 0, len(files))
	for i, file := range files {
		resp := uploadPrototypeFixture(t, handler, "?pbc=fee", file.name, file.data, RolePA)
		reviewStatus(t, resp, 201)
		var meta map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &meta); err != nil {
			t.Fatal(err)
		}
		imports = append(imports, meta)
		digest := artifacts.DigestOf(file.data)
		seq := len(previousFacts) + i + 1
		if meta["seq"] != float64(seq) || meta["digest"] != digest || meta["size"] != float64(len(file.data)) ||
			meta["fileName"] != file.name || meta["mediaType"] != file.mediaType || meta["by"] != "PA" || meta["at"] == "" || meta["at"] == nil {
			t.Fatalf("metadata mismatch: %v", meta)
		}
		if strings.HasPrefix(file.mediaType, "image/") {
			if meta["width"] != float64(12) || meta["height"] != float64(8) {
				t.Fatalf("image dimensions: %v", meta)
			}
		} else if meta["width"] != nil || meta["height"] != nil {
			t.Fatalf("unexpected dimensions: %v", meta)
		}
		got, err := repo.Get(digest)
		if err != nil || !bytes.Equal(got, file.data) {
			t.Fatalf("original missing from configured store: %v", err)
		}
		path := "/api/prototype-imports/" + strconv.Itoa(seq)
		reviewStatus(t, prototypeReviewRequest(handler, "GET", path+"?pbc=other", &bytes.Buffer{}, "", RoleSA, nil), 404)
		reviewStatus(t, prototypeReviewRequest(handler, "GET", path, &bytes.Buffer{}, "", RoleSA, nil), 400)
		read := prototypeReviewRequest(handler, "GET", path+"?pbc=fee", &bytes.Buffer{}, "", RoleSA, frame)
		reviewStatus(t, read, 200)
		if !bytes.Equal(read.Body.Bytes(), file.data) || !strings.HasPrefix(read.Header().Get("Content-Type"), file.mediaType) {
			t.Fatalf("wrong original or media type for %s", file.name)
		}
		if file.mediaType == "text/html" {
			if read.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(read.Header().Get("Content-Disposition"), "inline") {
				t.Fatalf("iframe headers: %v", read.Header())
			}
			csp := read.Header().Get("Content-Security-Policy")
			for _, rule := range []string{"sandbox allow-same-origin;", "default-src 'none'", "script-src 'none'", "form-action 'none'", "frame-ancestors 'self'", "style-src 'unsafe-inline'", "img-src data:", "font-src data:"} {
				if !strings.Contains(csp, rule) {
					t.Errorf("HTML CSP missing %q: %s", rule, csp)
				}
			}
			for _, ctx := range []struct {
				headers map[string]string
				want    string
			}{
				{nil, "inline"},
				{map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"}, "attachment"},
				{map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "cross-site"}, "attachment"},
			} {
				response := prototypeReviewRequest(handler, "GET", path+"?pbc=fee", &bytes.Buffer{}, "", RoleBA, ctx.headers)
				reviewStatus(t, response, 200)
				h := response.Header()
				if !strings.HasPrefix(h.Get("Content-Disposition"), ctx.want) || !bytes.Equal(response.Body.Bytes(), file.data) ||
					h.Get("Content-Security-Policy") != csp || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
					h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
					t.Fatalf("HTML response for %v (want %s): %v", ctx.headers, ctx.want, h)
				}
			}
		}
	}

	newFacts, err := s.Log.Load(t.Context(), "fee")
	if err != nil || len(newFacts) != len(previousFacts)+len(imports) {
		t.Fatalf("unexpected fact count %d: %v", len(newFacts), err)
	}
	for i, f := range newFacts[len(previousFacts):] {
		var payload map[string]any
		if err := json.Unmarshal(f.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if f.Seq != int(imports[i]["seq"].(float64)) || f.Type != "prototype.imported" ||
			f.Actor != "PA" || f.At != imports[i]["at"] ||
			payload["digest"] != imports[i]["digest"] || payload["seq"] != nil ||
			len(f.Payload) > 1024 {
			t.Fatalf("unexpected import fact: %+v (%s)", f, f.Payload)
		}
	}
	if current, err := s.Log.Load(t.Context(), "other"); err != nil || len(current) != 0 {
		t.Fatalf("unexpected other facts: %v, %v", current, err)
	}

	seq := len(previousFacts) + 1
	body := `{"importSeq":` + strconv.Itoa(seq) + `,"line":{"x1":1,"y1":0,"x2":0,"y2":0},"text":"水平线","by":"SA"}`
	for _, tt := range []struct {
		query, body string
		role        Role
		status      int
	}{
		{"?pbc=fee", body, RoleBA, 403},
		{"", body, RolePA, 400},
		{"?pbc=other", body, RolePA, 404},
		{"?pbc=fee", `{"importSeq":-1,"line":{"x1":1,"y1":0,"x2":0,"y2":0},"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"importSeq":null,"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"line":null,"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"importSeq":` + strconv.Itoa(seq) + `,"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"importSeq":` + strconv.Itoa(seq) + `,"line":{"x1":1,"y1":0,"x2":-0.1,"y2":0},"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"importSeq":` + strconv.Itoa(seq) + `,"line":{"x1":1,"y1":0,"x2":0},"text":"线"}`, RolePA, 400},
		{"?pbc=fee", `{"importSeq":` + strconv.Itoa(seq) + `,"line":{"x1":1,"y1":0,"x2":1,"y2":0},"text":"线"}`, RolePA, 400},
		{"?pbc=fee", body + ` {}`, RolePA, 400},
	} {
		resp := prototypeReviewRequest(handler, "POST", "/api/annotations"+tt.query, bytes.NewBufferString(tt.body), "application/json", tt.role, nil)
		reviewStatus(t, resp, tt.status)
	}
	line := prototypeReviewRequest(handler, "POST", "/api/annotations?pbc=fee", bytes.NewBufferString(body), "application/json", RolePA, nil)
	reviewStatus(t, line, 201)
	afterFacts, err := s.Log.Load(t.Context(), "fee")
	if err != nil || len(afterFacts) != len(newFacts)+1 || afterFacts[len(afterFacts)-1].Type != store.FactAnnotationAdded {
		t.Fatalf("unexpected annotation facts: %v, %v", afterFacts, err)
	}
	after := prototypeReviewRequest(handler, "GET", "/api/state?pbc=fee", &bytes.Buffer{}, "", RoleBA, nil)
	reviewStatus(t, after, 200)
	var oldState, newState map[string]any
	if err := json.Unmarshal(before.Body.Bytes(), &oldState); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after.Body.Bytes(), &newState); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"versions", "commits", "gates", "tags", "builds"} {
		if !reflect.DeepEqual(oldState[key], newState[key]) {
			t.Fatalf("read-only review changed %s", key)
		}
	}
	seen := newState["protoImports"].([]any)
	for i, meta := range imports {
		if !reflect.DeepEqual(seen[i], meta) {
			t.Fatalf("state metadata mismatch: %v vs %v", seen[i], meta)
		}
	}
	annos := newState["annos"].([]any)
	if len(annos) != 2 || !reflect.DeepEqual(annos[0], oldState["annos"].([]any)[0]) {
		t.Fatalf("legacy rectangle or count changed: %v", annos)
	}
	gotLine := annos[1].(map[string]any)
	if gotLine["importSeq"] != float64(seq) || gotLine["by"] != "PA" || !reflect.DeepEqual(gotLine["line"], map[string]any{"x1": float64(1), "y1": float64(0), "x2": float64(0), "y2": float64(0)}) {
		t.Fatalf("line not scoped to first import or coordinate order changed: %v", gotLine)
	}
	for _, bad := range []string{"0", "-1", "abc", "1", "10000"} {
		resp := prototypeReviewRequest(handler, "GET", "/api/prototype-imports/"+bad+"?pbc=fee", &bytes.Buffer{}, "", RoleBA, nil)
		want := 400
		if bad == "1" || bad == "10000" {
			want = 404
		}
		reviewStatus(t, resp, want)
	}

	hexDigest := strings.TrimPrefix(imports[0]["digest"].(string), "sha256:")
	if err := os.WriteFile(filepath.Join(repo.Root(), "blobs", "sha256", hexDigest), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := prototypeReviewRequest(handler, "GET", "/api/prototype-imports/"+strconv.Itoa(seq)+"?pbc=fee", &bytes.Buffer{}, "", RoleBA, nil)
	reviewStatus(t, corrupt, 500)
}

func TestPrototypeImportNotifiesSSESessions(t *testing.T) {
	_, _, handler, _ := prototypeReviewFixture(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var sessions []*bufio.Scanner
	for _, role := range []Role{RoleBA, RoleSA} {
		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/timeline/stream?pbc=fee", nil)
		if err != nil {
			t.Fatal(err)
		}
		cookie := httptest.NewRecorder()
		NewSessions([]byte("test-key")).Issue(cookie, role)
		req.AddCookie(cookie.Result().Cookies()[0])
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		stream := bufio.NewScanner(resp.Body)
		for _, line := range []string{"event: hello", "data: {}", ""} {
			if !stream.Scan() || stream.Text() != line {
				t.Fatalf("SSE hello = %q, want %q (%v)", stream.Text(), line, stream.Err())
			}
		}
		sessions = append(sessions, stream)
	}
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "review.html", []byte("<h1>原型</h1>"), RolePA), 201)
	for _, stream := range sessions {
		for _, line := range []string{"event: change", `data: {"data":null,"pbc":"fee","type":"fact.appended"}`} {
			if !stream.Scan() || stream.Text() != line {
				t.Fatalf("SSE change = %q, want %q (%v)", stream.Text(), line, stream.Err())
			}
		}
	}
}

func TestPrototypeImportLineSurvivesRedisPrecision(t *testing.T) {
	s, _, handler, sidecar := prototypeReviewFixture(t)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "review.html", []byte("<h1>原型</h1>"), RolePA), 201)
	const longLine = `{"importSeq":1,"line":{"x1":-0,"y1":0.28000000000000003,"x2":0.6199999999999999,"y2":0.28000000000000003},"text":"水平线"}`
	msg := prototypeReviewRequest(handler, "POST", "/api/annotations?pbc=fee", bytes.NewBufferString(longLine), "application/json", RolePA, nil)
	reviewStatus(t, msg, 201)

	// 在假 sidecar 中按实测的 Redis 读回结果重编码坐标，保留事实哈希。
	rewrite := strings.NewReplacer(
		`"x1":-0`, `"x1":0`,
		"0.28000000000000003", "0.28",
		"0.6199999999999999", "0.62",
	)
	sidecar.mu.Lock()
	for key, raw := range sidecar.data {
		if strings.HasPrefix(key, "log:fee:00000002:") {
			sidecar.data[key] = []byte(rewrite.Replace(string(raw)))
		}
	}
	sidecar.mu.Unlock()

	facts, err := s.Log.Load(t.Context(), "fee")
	if err != nil || len(facts) != 2 {
		t.Fatalf("annotation failed ledger replay after Redis numeric encoding: %v, %v", facts, err)
	}
	var saved struct {
		Line map[string]float64 `json:"line"`
	}
	if strings.Contains(msg.Body.String(), `"x1":-0`) {
		t.Fatal("response retained negative zero")
	}
	if err := json.Unmarshal(facts[1].Payload, &saved); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"x1": 0, "y1": 0.28, "x2": 0.62, "y2": 0.28}
	if !reflect.DeepEqual(saved.Line, want) {
		t.Fatalf("stored numeric line = %v, want %v", saved.Line, want)
	}
	const tinyLine = `{"importSeq":1,"line":{"x1":0.24,"y1":0.28,"x2":0.24000001,"y2":0.28},"text":"端点太近"}`
	msg = prototypeReviewRequest(handler, "POST", "/api/annotations?pbc=fee", bytes.NewBufferString(tinyLine), "application/json", RolePA, nil)
	reviewStatus(t, msg, 400)
	if facts, err := s.Log.Load(t.Context(), "fee"); err != nil || len(facts) != 2 {
		t.Fatalf("post-round degeneracy changed ledger: %v, %v", facts, err)
	}
}

type blockedPrototypeReader struct {
	io.Reader
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (r *blockedPrototypeReader) Read(b []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.resume
	return r.Reader.Read(b)
}

func TestPrototypeImportRejectsRegistryReplacement(t *testing.T) {
	s, repo, handler, _ := prototypeReviewFixture(t)
	data := []byte("<h1>旧登记的原型</h1>")
	body, contentType := prototypeMultipart(t, "review.html", data)
	blocked := &blockedPrototypeReader{Reader: body, started: make(chan struct{}), resume: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(blocked.resume) })
	defer resume()
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		completed <- prototypeReviewRequest(handler, "POST", "/api/prototype-imports?pbc=fee", blocked, contentType, RolePA, nil)
	}()
	select {
	case <-blocked.started:
	case resp := <-completed:
		t.Fatalf("upload returned before reading the body: %d %s", resp.Code, resp.Body)
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start reading the body")
	}

	replaced := make(chan [2]*httptest.ResponseRecorder, 1)
	go func() {
		removed := prototypeReviewRequest(handler, "DELETE", "/api/units/fee?createdSeq=1", nil, "", RolePA, nil)
		recreated := prototypeReviewRequest(handler, "POST", "/api/units",
			strings.NewReader(`{"id":"fee","name":"新登记","kind":"pbc"}`), "application/json", RolePA, nil)
		replaced <- [2]*httptest.ResponseRecorder{removed, recreated}
	}()
	var active struct {
		RegistrySeq int `json:"registrySeq"`
	}
	select {
	case responses := <-replaced:
		reviewStatus(t, responses[0], 200)
		reviewStatus(t, responses[1], 201)
		if err := json.Unmarshal(responses[1].Body.Bytes(), &active); err != nil || active.RegistrySeq <= 1 {
			t.Fatalf("replacement registration: %s (%v)", responses[1].Body, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading the upload body blocked registry changes")
	}
	resume()
	select {
	case resp := <-completed:
		reviewStatus(t, resp, 409)
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish")
	}

	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee&createdSeq=1", "stale.html", data, RolePA), 409)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee&createdSeq=0", "invalid.html", data, RolePA), 400)
	if blobs, err := repo.List(); err != nil || len(blobs) != 0 {
		t.Fatalf("rejected upload wrote blobs: %v, %v", blobs, err)
	}
	if facts, err := s.Log.Load(t.Context(), "fee"); err != nil || len(facts) != 1 || facts[0].Type != store.FactUnitCreated {
		t.Fatalf("only the replacement unit creation should be recorded: %v, %v", facts, err)
	}
	query := "?pbc=fee&createdSeq=" + strconv.Itoa(active.RegistrySeq)
	reviewStatus(t, uploadPrototypeFixture(t, handler, query, "new.html", data, RolePA), 201)
}

func prototypePDFPage(xrefStream bool) []byte {
	const content = "0 0 1 rg\n10 10 40 40 re f\n"
	return prototypePDFWithPageStream(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content), xrefStream)
}

func prototypePDFWithPageStream(content string, xrefStream bool) []byte {
	return prototypePDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 120 80] /Resources << >> /Contents 4 0 R >>",
		content,
	}, xrefStream)
}

func prototypePDF(objects []string, xrefStream bool) []byte {
	var data bytes.Buffer
	data.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, 0, len(objects)+1)
	for i, obj := range objects {
		offsets = append(offsets, data.Len())
		fmt.Fprintf(&data, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := data.Len()
	if xrefStream {
		offsets = append(offsets, xref)
		fmt.Fprintf(&data, "%d 0 obj\n<< /Type /XRef /Size %d /Root 1 0 R /W [1 4 2] /Length %d >>\nstream\n",
			len(offsets), len(offsets)+1, 7*(len(offsets)+1))
		data.Write([]byte{0, 0, 0, 0, 0, 255, 255})
		for _, offset := range offsets {
			entry := [7]byte{1}
			binary.BigEndian.PutUint32(entry[1:5], uint32(offset))
			data.Write(entry[:])
		}
		data.WriteString("\nendstream\nendobj\n")
	} else {
		fmt.Fprintf(&data, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
		for _, offset := range offsets {
			fmt.Fprintf(&data, "%010d 00000 n \n", offset)
		}
		fmt.Fprintf(&data, "trailer\n<< /Size %d /Root 1 0 R >>\n", len(offsets)+1)
	}
	fmt.Fprintf(&data, "startxref\n%d\n%%%%EOF\n", xref)
	return data.Bytes()
}

func TestPrototypeImportPDFStructure(t *testing.T) {
	s, repo, handler, _ := prototypeReviewFixture(t)
	var compressed bytes.Buffer
	zip := zlib.NewWriter(&compressed)
	if _, err := zip.Write([]byte("0 0 1 rg\n10 10 40 40 re f\n")); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	badStream := compressed.Bytes()
	badStream[len(badStream)-1] ^= 1
	brokenPage := prototypePDFWithPageStream(fmt.Sprintf(
		"<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", len(badStream), badStream,
	), false)
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"header and EOF", []byte("%PDF-1.7\n%%EOF\n")},
		{"bad xref offset", []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\nstartxref\n9\n%%EOF\n")},
		{"catalog only", prototypePDF([]string{"<< /Type /Catalog >>"}, false)},
		{"empty page tree", prototypePDF([]string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 0 /Kids [] >>"}, false)},
		{"broken page content checksum", brokenPage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := uploadPrototypeFixture(t, handler, "?pbc=fee", "invalid.pdf", tt.data, RolePA)
			reviewStatus(t, resp, 400)
			if !strings.Contains(resp.Body.String(), "PDF") {
				t.Fatalf("PDF validation error: %s", resp.Body)
			}
		})
	}
	for _, password := range []string{"", "preview-password"} {
		conf := model.NewStatelessConfiguration()
		conf.UserPW, conf.OwnerPW = password, "owner-password"
		var encrypted bytes.Buffer
		if err := api.Encrypt(t.Context(), bytes.NewReader(prototypePDFPage(false)), &encrypted, conf); err != nil {
			t.Fatal(err)
		}
		resp := uploadPrototypeFixture(t, handler, "?pbc=fee", "encrypted.pdf", encrypted.Bytes(), RolePA)
		reviewStatus(t, resp, 400)
		if !strings.Contains(resp.Body.String(), "加密") {
			t.Fatalf("encrypted PDF error: %s", resp.Body)
		}
	}
	if blobs, err := repo.List(); err != nil || len(blobs) != 0 {
		t.Fatalf("invalid PDFs wrote blobs: %v, %v", blobs, err)
	}
	if facts, err := s.Log.Load(t.Context(), "fee"); err != nil || len(facts) != 0 {
		t.Fatalf("invalid PDFs wrote facts: %v, %v", facts, err)
	}
	for _, stream := range []bool{false, true} {
		reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "page.pdf", prototypePDFPage(stream), RolePA), 201)
	}
}

func TestNativeHTMLPreviewKeepsOriginalAndScopesScriptPermission(t *testing.T) {
	_, _, handler, _ := prototypeReviewFixture(t)
	source := []byte(`<!doctype html><p id="page">首页</p><script>document.getElementById('page').textContent = '列表'</script>`)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "native.html", source, RolePA), http.StatusCreated)
	read := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		return prototypeReviewRequest(handler, "GET", path, nil, "", RoleBA, headers)
	}
	const route = "/api/prototype-imports/1/native"
	frame := map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin"}
	reviewStatus(t, read(route, frame), http.StatusBadRequest)
	reviewStatus(t, read(route+"?pbc=another", frame), http.StatusNotFound)
	native := read(route+"?pbc=fee", frame)
	reviewStatus(t, native, http.StatusOK)
	if !bytes.Equal(native.Body.Bytes(), source) ||
		!strings.HasPrefix(native.Header().Get("Content-Disposition"), "inline") ||
		native.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("交互预览必须提供无缓存的原件")
	}
	csp := native.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts allow-forms") || strings.Contains(csp, "allow-same-origin") ||
		!strings.Contains(csp, "script-src 'unsafe-inline'") || !strings.Contains(csp, "form-action 'none'") {
		t.Fatalf("交互沙箱允许本地脚本和表单事件，保持独立页权限：%s", csp)
	}
	top := read(route+"?pbc=fee", map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"})
	reviewStatus(t, top, http.StatusOK)
	if !strings.HasPrefix(top.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("单独访问应按原件下载处理")
	}
	snapshotSource := read("/api/prototype-imports/1?pbc=fee", frame)
	reviewStatus(t, snapshotSource, http.StatusOK)
	if !bytes.Equal(snapshotSource.Body.Bytes(), source) || !strings.Contains(snapshotSource.Header().Get("Content-Security-Policy"), "script-src 'none'") {
		t.Fatal("评审原件仍走禁用脚本的路径")
	}
}
