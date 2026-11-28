package bff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/zhangkun/i3-composer/internal/store"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	prototypeFileLimit = 20 << 20
	prototypeFormLimit = prototypeFileLimit + (64 << 10)
	prototypeMaxSide   = 8192
	prototypeMaxPixels = 16 << 20
	prototypeHTMLCSP   = "sandbox allow-same-origin; default-src 'none'; base-uri 'none'; script-src 'none'; " +
		"style-src 'unsafe-inline'; img-src data:; font-src data:; form-action 'none'; frame-ancestors 'self'"
)

var errUnitRegistrationChanged = errors.New("单元登记已变化，请刷新后重新上传")

func (s *Server) uploadPrototypeImport(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActAcceptProto); err != nil {
		return err
	}
	if err := requireExplicitReviewUnit(r); err != nil {
		return err
	}
	registration, err := s.prototypeUploadRegistration(r, c.PBC)
	if err != nil {
		return err
	}
	data, name, err := readPrototypeUpload(w, r)
	if err != nil {
		return err
	}
	meta, err := validatePrototypeFile(r.Context(), data)
	if err != nil {
		return err
	}
	meta.FileName = name
	if s.Pipe == nil || s.Pipe.Store == nil {
		return errors.New("原件制品仓库未配置")
	}

	// 作废后同 ID 可复用，写入前必须确认还在上传开始时的登记上。
	s.unitRegistryMu.Lock()
	defer s.unitRegistryMu.Unlock()
	current, err := s.registeredUnit(r.Context(), c.PBC)
	if errors.Is(err, errNotFound) {
		return errUnitRegistrationChanged
	}
	if err != nil {
		return err
	}
	if current.RegistrySeq != registration {
		return errUnitRegistrationChanged
	}
	meta.Digest, meta.Size, err = s.Pipe.Store.Put(data)
	if err != nil {
		return fmt.Errorf("保存原型原件失败：%w", err)
	}
	// 有原件才落链。追加失败时保留按摘要存放的字节，避免误删其他导入引用的同一份原件。
	fact, err := s.Log.Append(r.Context(), c.PBC, store.FactPrototypeImported, c.Role.String(), meta)
	if err != nil {
		return err
	}
	// Append 落链后的 Log.Notify 已向所有 SSE 订阅会话发送刷新提示。
	meta.Seq, meta.By, meta.At = fact.Seq, fact.Actor, fact.At
	writeJSON(w, http.StatusCreated, meta)
	return nil
}

func (s *Server) prototypeUploadRegistration(r *http.Request, pbc string) (int, error) {
	var expected int
	if values, present := r.URL.Query()["createdSeq"]; present {
		var err error
		expected, err = strconv.Atoi(values[0])
		if len(values) != 1 || err != nil || expected < 1 {
			return 0, fmt.Errorf("%w：createdSeq 必须是正整数登记序号", errBadRequest)
		}
	}

	// 只在读取登记时持锁，慢上传和格式校验不能阻塞单元作废或复登。
	s.unitRegistryMu.Lock()
	defer s.unitRegistryMu.Unlock()
	unit, err := s.registeredUnit(r.Context(), pbc)
	if err != nil {
		return 0, err
	}
	if expected > 0 && unit.RegistrySeq != expected {
		return 0, errUnitRegistrationChanged
	}
	return unit.RegistrySeq, nil
}

func requireExplicitReviewUnit(r *http.Request) error {
	ids := r.URL.Query()["pbc"]
	if len(ids) != 1 || strings.TrimSpace(ids[0]) == "" {
		return fmt.Errorf("%w：需要 ?pbc=<单元 id>", errBadRequest)
	}
	return nil
}

func readPrototypeUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, prototypeFormLimit)
	form, err := r.MultipartReader()
	if err != nil {
		return nil, "", fmt.Errorf("%w：请通过 multipart 的 file 字段上传原型", errBadRequest)
	}
	file, err := form.NextRawPart()
	if err != nil {
		return nil, "", prototypeUploadError(err)
	}
	name := path.Base(strings.ReplaceAll(file.FileName(), "\\", "/"))
	if file.FormName() != "file" || name == "." || name == ".." || name == "/" ||
		len(name) > 255 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return nil, "", fmt.Errorf("%w：需要一个带有效文件名的 file 字段", errBadRequest)
	}
	data, err := io.ReadAll(io.LimitReader(file, prototypeFileLimit+1))
	if len(data) > prototypeFileLimit {
		return nil, "", fmt.Errorf("%w：原型文件不能超过 20 MiB", errTooLarge)
	}
	if err != nil {
		return nil, "", prototypeUploadError(err)
	}
	// 读到结束分隔符才确认收到了完整的表单。
	if _, err := form.NextRawPart(); err != io.EOF {
		return nil, "", prototypeUploadError(err)
	}
	return data, name, nil
}

func prototypeUploadError(err error) error {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return fmt.Errorf("%w：原型文件不能超过 20 MiB", errTooLarge)
	}
	return fmt.Errorf("%w：上传表单必须包含且仅包含一个完整文件", errBadRequest)
}

func validatePrototypeFile(ctx context.Context, data []byte) (store.PrototypeImport, error) {
	switch mediaType := http.DetectContentType(data); mediaType {
	case "image/png":
		return validatePrototypeImage(data, mediaType, png.DecodeConfig, png.Decode)
	case "image/jpeg":
		return validatePrototypeImage(data, mediaType, jpeg.DecodeConfig, jpeg.Decode)
	case "application/pdf":
		if err := validatePrototypePDF(ctx, data); err != nil {
			return store.PrototypeImport{}, err
		}
		return store.PrototypeImport{MediaType: mediaType}, nil
	}
	if validPrototypeHTML(data) {
		return store.PrototypeImport{MediaType: "text/html"}, nil
	}
	return store.PrototypeImport{}, fmt.Errorf("%w：原型文件格式不受支持，仅支持 PNG、JPEG、PDF 和 UTF-8 HTML", errBadRequest)
}

func validatePrototypeImage(data []byte, mediaType string, config func(io.Reader) (image.Config, error), decode func(io.Reader) (image.Image, error)) (store.PrototypeImport, error) {
	dims, err := config(bytes.NewReader(data))
	if err != nil {
		return store.PrototypeImport{}, fmt.Errorf("%w：图片头无效", errBadRequest)
	}
	// 先按轴和像素总量限额，再完整解码，避免小文件声明超大画布。
	if dims.Width <= 0 || dims.Height <= 0 || dims.Width > prototypeMaxSide ||
		dims.Height > prototypeMaxSide || dims.Width*dims.Height > prototypeMaxPixels {
		return store.PrototypeImport{}, fmt.Errorf("%w：图片最多 8192 像素一边、16777216 像素总量", errBadRequest)
	}
	img, err := decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != dims.Width || img.Bounds().Dy() != dims.Height {
		return store.PrototypeImport{}, fmt.Errorf("%w：图片内容不完整或尺寸不一致", errBadRequest)
	}
	return store.PrototypeImport{MediaType: mediaType, Width: dims.Width, Height: dims.Height}, nil
}

var prototypePDFTrailer = regexp.MustCompile(`(?:^|[\r\n])startxref[ \t]*[\r\n]+[ \t]*([0-9]+)[ \t]*[\r\n]+%%EOF$`)

func validatePrototypePDF(ctx context.Context, data []byte) error {
	if len(data) < len("%PDF-1.0\n%%EOF") || !bytes.HasPrefix(data, []byte("%PDF-")) ||
		(data[5] != '1' && data[5] != '2') || data[6] != '.' || data[7] < '0' || data[7] > '9' ||
		(data[8] != '\n' && data[8] != '\r') {
		return fmt.Errorf("%w：PDF 文件头无效", errBadRequest)
	}
	end := bytes.TrimRight(data, "\x00\t\n\f\r ")
	match := prototypePDFTrailer.FindSubmatch(end[max(0, len(end)-1024):])
	if match == nil {
		return fmt.Errorf("%w：PDF 缺少有效的 startxref 或结束标记", errBadRequest)
	}
	offset, err := strconv.Atoi(string(match[1]))
	if err != nil || offset < 9 || offset >= len(end) {
		return fmt.Errorf("%w：PDF 交叉引用偏移无效", errBadRequest)
	}

	// 只解析结构和首页内容流，限制对象、解压和遍历开销；实际绘制留给浏览器。
	conf := model.NewStatelessConfiguration()
	conf.Offline = true
	conf.Limits = model.ResourceLimits{
		MaxInputBytes:        prototypeFileLimit,
		MaxObjectBytes:       2 << 20,
		MaxStreamBytes:       prototypeFileLimit,
		MaxDecodeBytes:       16 << 20,
		MaxObjectCount:       100_000,
		MaxObjectStreamCount: 10_000,
		MaxObjectStreamFirst: 1 << 20,
		MaxXRefEntries:       100_000,
		MaxRecursionDepth:    64,
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	doc, err := api.ReadContext(ctx, bytes.NewReader(data), conf)
	switch {
	case errors.Is(err, pdfcpu.ErrWrongPassword), errors.Is(err, pdfcpu.ErrUnsupportedEncryptionFeature),
		errors.Is(err, pdfcpu.ErrPermissionDenied), err == nil && doc.Encrypt != nil:
		return fmt.Errorf("%w：请上传未加密的 PDF", errBadRequest)
	case err != nil:
		return fmt.Errorf("%w：PDF 交叉引用或文档结构无效，或超出解析限制", errBadRequest)
	}
	if err := doc.EnsurePageCount(); err != nil {
		return fmt.Errorf("%w：PDF 缺少有效的页面树", errBadRequest)
	}
	page, _, attrs, err := doc.PageDict(ctx, 1, false)
	if err != nil || attrs.MediaBox == nil ||
		!(attrs.MediaBox.Width() > 0 && attrs.MediaBox.Width() < math.Inf(1)) ||
		!(attrs.MediaBox.Height() > 0 && attrs.MediaBox.Height() < math.Inf(1)) {
		return fmt.Errorf("%w：PDF 缺少尺寸有效的首页", errBadRequest)
	}
	// 读取结构时兼容常见的 PDF 变体，解码首页时要求损坏的内容流明确报错。
	doc.XRefTable.ValidationMode = model.ValidationStrict
	if _, err := doc.PageContent(page, 1); err != nil && !errors.Is(err, model.ErrNoContent) {
		return fmt.Errorf("%w：PDF 首页内容流损坏或超出解析限制", errBadRequest)
	}
	return nil
}

func validPrototypeHTML(data []byte) bool {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	z := html.NewTokenizer(bytes.NewReader(data))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.DoctypeToken:
			name := strings.TrimSpace(z.Token().Data)
			if i := strings.IndexAny(name, " \t\n\f\r"); i >= 0 {
				name = name[:i]
			}
			if strings.EqualFold(name, "html") {
				return true
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			tag, _ := z.TagName()
			switch atom.Lookup(tag) {
			case atom.Svg, atom.Math:
				// 内嵌图形可随 HTML 预览，独立的 XML 图形不属于导入格式。
				return false
			case 0:
			default:
				return true
			}
		}
	}
}

func (s *Server) scopedPrototypeImport(r *http.Request, pbc string, seq int) (store.PrototypeImport, error) {
	if err := requireExplicitReviewUnit(r); err != nil {
		return store.PrototypeImport{}, err
	}
	if err := s.requireRegisteredUnit(r.Context(), pbc); err != nil {
		return store.PrototypeImport{}, err
	}
	v, err := s.load(r, pbc)
	if err != nil {
		return store.PrototypeImport{}, err
	}
	for _, meta := range v.ProtoImports {
		if meta.Seq == seq {
			return meta, nil
		}
	}
	return store.PrototypeImport{}, fmt.Errorf("%w：此单元没有序号 %d 的原型导入", errNotFound, seq)
}

func (s *Server) readPrototypeImport(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActView); err != nil {
		return err
	}
	rawSeq := r.PathValue("seq")
	seq, err := strconv.Atoi(rawSeq)
	if err != nil || seq <= 0 || strconv.Itoa(seq) != rawSeq {
		return fmt.Errorf("%w：需要正整数导入序号", errBadRequest)
	}
	meta, err := s.scopedPrototypeImport(r, c.PBC, seq)
	if err != nil {
		return err
	}
	if s.Pipe == nil || s.Pipe.Store == nil {
		return errors.New("原件制品仓库未配置")
	}
	data, err := s.Pipe.Store.Get(meta.Digest)
	if err != nil {
		return fmt.Errorf("读取原型原件失败：%w", err)
	}
	if int64(len(data)) != meta.Size {
		return errors.New("原型原件与账本字节数不符")
	}

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	mediaType, disposition := meta.MediaType, "attachment"
	switch mediaType {
	case "image/png", "image/jpeg":
		disposition = "inline"
	case "text/html":
		mediaType += "; charset=utf-8"
		h.Set("Content-Security-Policy", prototypeHTMLCSP)
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-DNS-Prefetch-Control", "off")
		h.Set("Referrer-Policy", "no-referrer")
		h.Add("Vary", "Sec-Fetch-Dest, Sec-Fetch-Mode, Sec-Fetch-Site")
		dest := r.Header.Get("Sec-Fetch-Dest")
		mode := r.Header.Get("Sec-Fetch-Mode")
		site := r.Header.Get("Sec-Fetch-Site")
		// 内网 HTTP 可能没有 Fetch Metadata；两种内联路径共用同一套沙箱。
		if (dest == "" && mode == "" && site == "") ||
			(dest == "iframe" && mode == "navigate" && site == "same-origin") {
			disposition = "inline"
		}
	case "application/pdf":
		// 在线评审由 PDF.js 读取字节，直接导航保留原件下载。
	default:
		return errors.New("账本中的原型媒体类型不受支持")
	}
	h.Set("Content-Type", mediaType)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": meta.FileName}))
	http.ServeContent(w, r, meta.FileName, time.Time{}, bytes.NewReader(data))
	return nil
}

func decodePrototypeLine(raw json.RawMessage) (*store.AnnoLine, error) {
	var p struct{ X1, Y1, X2, Y2 *float64 }
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w：批注需要画线坐标 x1、y1、x2、y2", errBadRequest)
	}
	for _, coordinate := range []*float64{p.X1, p.Y1, p.X2, p.Y2} {
		if coordinate == nil || *coordinate < 0 || *coordinate > 1 {
			return nil, fmt.Errorf("%w：画线的四个坐标都必须介于 0 和 1 之间", errBadRequest)
		}
		// 状态存储会重编码 JSON 数值，落链前留六位小数，并用整数中间值统一 -0 和 0。
		const precision = 1_000_000
		*coordinate = float64(int(math.Round(*coordinate*precision))) / precision
	}
	if *p.X1 == *p.X2 && *p.Y1 == *p.Y2 {
		return nil, fmt.Errorf("%w：画线的两个端点不能重合", errBadRequest)
	}
	return &store.AnnoLine{X1: *p.X1, Y1: *p.Y1, X2: *p.X2, Y2: *p.Y2}, nil
}
