package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//go:embed web/*
var materialWeb embed.FS

type sourceOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type typeOption struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	AssetOwner  string         `json:"assetOwner"`
	FinanceTag  string         `json:"financeTag"`
	SourceLabel string         `json:"sourceLabel"`
	Sources     []sourceOption `json:"sources"`
}

var receiptTypes = []typeOption{
	{ID: "RKT-01", Name: "采购入库", AssetOwner: "公司资产", FinanceTag: "一次性进成本", SourceLabel: "采购合同",
		Sources: []sourceOption{{ID: "CG-TEST-001", Name: "采购合同样例"}}},
	{ID: "RKT-02", Name: "甲供入库", AssetOwner: "甲方资产", FinanceTag: "一次性进成本", SourceLabel: "到货点验",
		Sources: []sourceOption{{ID: "DY-TEST-001", Name: "到货点验样例"}}},
}

type materialOption struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Unit          string `json:"unit"`
	WarehouseID   string `json:"warehouseId"`
	WarehouseName string `json:"warehouseName"`
	RemainingQty  string `json:"remainingQty"`
}

const initialPlan = 120_000

var steel = materialOption{Code: "MAT-STEEL", Name: "钢材", Unit: "吨", WarehouseID: "WH-01", WarehouseName: "材料库房"}

type optionsView struct {
	Unit         string           `json:"unit"`
	Commit       string           `json:"commit"`
	Environment  string           `json:"environment"`
	Types        []typeOption     `json:"types"`
	Materials    []materialOption `json:"materials"`
	ReceiptCount int              `json:"receiptCount"`
}

type inboundInput struct {
	RequestID    string `json:"requestId"`
	Type         string `json:"type"`
	SourceNo     string `json:"sourceNo"`
	MaterialCode string `json:"materialCode"`
	WarehouseID  string `json:"warehouseId"`
	Quantity     string `json:"quantity"`
	Unit         string `json:"unit"`
}

type receipt struct {
	ReceiptNo     string `json:"receiptNo"`
	Type          string `json:"type"`
	SourceNo      string `json:"sourceNo"`
	SourceLabel   string `json:"sourceLabel"`
	MaterialCode  string `json:"materialCode"`
	MaterialName  string `json:"materialName"`
	WarehouseID   string `json:"warehouseId"`
	WarehouseName string `json:"warehouseName"`
	Quantity      string `json:"quantity"`
	Unit          string `json:"unit"`
	AssetOwner    string `json:"assetOwner"`
	FinanceTag    string `json:"financeTag"`
	RemainingQty  string `json:"remainingQty"`
}

type confirmation struct {
	Event   string       `json:"type"`
	Input   inboundInput `json:"input"`
	Receipt receipt      `json:"receipt"`
}

type ledger struct {
	mu        sync.Mutex
	path      string
	events    []confirmation
	remaining int64
}

func openLedger(path string) (*ledger, error) {
	l := &ledger{path: path, remaining: initialPlan}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.events); err != nil {
		return nil, fmt.Errorf("重放入库事实：%w", err)
	}
	for _, e := range l.events {
		q, ok := milli(e.Receipt.Quantity)
		if e.Event != "inbound.confirmed" || !ok || q > l.remaining || e.Receipt.RemainingQty != qty(l.remaining-q) {
			return nil, fmt.Errorf("入库事实 %s 的计划量不一致", e.Receipt.ReceiptNo)
		}
		l.remaining -= q
	}
	return l, nil
}

var decimal = regexp.MustCompile(`^[0-9]{1,8}(\.[0-9]{1,3})?$`)
var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func milli(text string) (int64, bool) {
	if !decimal.MatchString(text) {
		return 0, false
	}
	whole, fractional, _ := strings.Cut(text, ".")
	a, _ := strconv.ParseInt(whole, 10, 64)
	b, _ := strconv.ParseInt((fractional + "000")[:3], 10, 64)
	n := a*1000 + b
	return n, n > 0
}

func qty(n int64) string { return fmt.Sprintf("%d.%03d", n/1000, n%1000) }

func receiptType(id string) typeOption {
	for _, t := range receiptTypes {
		if t.ID == id {
			return t
		}
	}
	return typeOption{}
}

func validSource(t typeOption, sourceNo string) bool {
	for _, source := range t.Sources {
		if source.ID == sourceNo {
			return true
		}
	}
	return false
}

func newHandler(path, env, commit string) (http.Handler, error) {
	l, err := openLedger(path)
	if err != nil {
		return nil, err
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]any{"ok": true, "unit": unitID, "commit": commit, "environment": env})
	})
	m.HandleFunc("GET /api/options", func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		mat := steel
		mat.RemainingQty = qty(l.remaining)
		jsonReply(w, 200, optionsView{Unit: unitID, Commit: commit, Environment: env,
			Types: receiptTypes, Materials: []materialOption{mat}, ReceiptCount: len(l.events)})
	})
	m.HandleFunc("POST /api/inbounds", l.confirm)
	m.HandleFunc("GET /api/inbounds", l.list)
	m.HandleFunc("GET /api/inbounds/{id}", l.detail)
	web, err := fs.Sub(materialWeb, "web")
	if err != nil {
		return nil, err
	}
	m.Handle("GET /", http.FileServerFS(web))
	return m, nil
}

func (l *ledger) confirm(w http.ResponseWriter, r *http.Request) {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	d.DisallowUnknownFields()
	var in inboundInput
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "入库单格式无效")
		return
	}
	n, ok := milli(in.Quantity)
	t := receiptType(in.Type)
	switch {
	case !ok:
		fail(w, 400, "入库数量必须为正数，最多三位小数")
	case !requestID.MatchString(in.RequestID):
		fail(w, 400, "缺少有效的提交编号")
	case t.ID == "" || !validSource(t, in.SourceNo):
		fail(w, 400, "来源单号与入库类型不匹配")
	case in.MaterialCode != steel.Code || in.WarehouseID != steel.WarehouseID || in.Unit != steel.Unit:
		fail(w, 400, "材料、单位与库房必须符合当前计划")
	default:
		l.appendReceipt(w, in, t, n)
	}
}

func (l *ledger) appendReceipt(w http.ResponseWriter, in inboundInput, t typeOption, n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	in.Quantity = qty(n)
	for _, e := range l.events {
		if e.Input.RequestID == in.RequestID {
			if e.Input != in {
				fail(w, 409, "该提交编号已用于另一张入库单")
				return
			}
			jsonReply(w, 200, e.Receipt)
			return
		}
	}
	if n > l.remaining {
		fail(w, 409, fmt.Sprintf("超计划：剩余 %s %s，请调整入库数量或需用计划", qty(l.remaining), steel.Unit))
		return
	}
	row := receipt{ReceiptNo: fmt.Sprintf("RK-%06d", len(l.events)+1), Type: t.ID,
		SourceNo: in.SourceNo, SourceLabel: t.SourceLabel, MaterialCode: steel.Code, MaterialName: steel.Name,
		WarehouseID: steel.WarehouseID, WarehouseName: steel.WarehouseName, Quantity: in.Quantity, Unit: steel.Unit,
		AssetOwner: t.AssetOwner, FinanceTag: t.FinanceTag, RemainingQty: qty(l.remaining - n)}
	next := append(l.events, confirmation{Event: "inbound.confirmed", Input: in, Receipt: row})
	if err := persist(l.path, next); err != nil {
		fail(w, 500, "未能写入入库账本，请重试")
		return
	}
	l.events, l.remaining = next, l.remaining-n
	jsonReply(w, 201, row)
}

// 单条确认事实同时固化明细与余量。先落盘再更新投影，失败时两者都保持原值。
func persist(path string, events []confirmation) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".receipts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(events)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (l *ledger) list(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows := make([]receipt, 0, len(l.events))
	for i := len(l.events) - 1; i >= 0; i-- {
		rows = append(rows, l.events[i].Receipt)
	}
	jsonReply(w, 200, map[string]any{"receipts": rows})
}

func (l *ledger) detail(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		if e.Receipt.ReceiptNo == r.PathValue("id") {
			jsonReply(w, 200, e.Receipt)
			return
		}
	}
	fail(w, 404, "未找到该入库单")
}

func jsonReply(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func fail(w http.ResponseWriter, status int, message string) {
	jsonReply(w, status, map[string]string{"error": message})
}
