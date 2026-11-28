package bff

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPrototypeBoxesReplayByImportAndPage(t *testing.T) {
	_, _, handler, _ := prototypeReviewFixture(t)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "warehouse.html", []byte("<h1>仓库</h1>"), RolePA), http.StatusCreated)
	post := func(query string, payload string, role Role, status int) {
		t.Helper()
		r := prototypeReviewRequest(handler, "POST", "/api/annotations"+query, strings.NewReader(payload), "application/json", role, nil)
		reviewStatus(t, r, status)
	}
	const box = `{"importSeq":1,"x":0.12345678,"y":0.2,"w":0.3,"h":0.4,"text":"到货表头"`
	post("?pbc=fee", box+`,"preview":{"route":"#/material/inbound-hub","anchor":"0/1/2"}}`, RolePA, http.StatusCreated)
	post("?pbc=fee", box+`}`, RolePA, http.StatusCreated)
	post("?pbc=fee", box+`,"preview":{"route":"","anchor":""}}`, RolePA, http.StatusCreated)
	for _, invalid := range []string{
		box + `,"preview":null}`,
		box + `,"preview":{"route":"https://invalid.local","anchor":""}}`,
		box + `,"preview":{"route":"#/tab","anchor":"div[title]"}}`,
		box + `,"preview":{"route":"#/tab"}}`,
		`{"importSeq":1,"x":0.8,"y":0.1,"w":0.3,"h":0.2,"text":"越界"}`,
		`{"importSeq":1,"x":0.1,"y":0.1,"w":0.3,"h":0,"text":"空框"}`,
		`{"importSeq":1,"line":{"x1":0,"y1":0,"x2":1,"y2":1},"preview":{"route":"#/tab","anchor":""},"text":"类型不符"}`,
	} {
		post("?pbc=fee", invalid, RolePA, http.StatusBadRequest)
	}
	post("?pbc=fee", box+`}`, RoleBA, http.StatusForbidden)
	post("?pbc=other", box+`}`, RolePA, http.StatusNotFound)

	response := prototypeReviewRequest(handler, "GET", "/api/state?pbc=fee", nil, "", RoleBA, nil)
	reviewStatus(t, response, http.StatusOK)
	var replay struct {
		Annos []map[string]any `json:"annos"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Annos) != 3 {
		t.Fatalf("拒绝的请求不落链，当前批注 %d", len(replay.Annos))
	}
	first := replay.Annos[0]
	scope, ok := first["preview"].(map[string]any)
	if !ok || scope["route"] != "#/material/inbound-hub" || scope["anchor"] != "0/1/2" ||
		first["importSeq"] != float64(1) || first["x"] != 0.123457 || first["w"] != 0.3 || first["by"] != "PA" {
		t.Fatalf("回放应保留盒子、导入来源及页面定位：%#v", first)
	}
	if _, ok := replay.Annos[1]["preview"]; ok {
		t.Fatal("只读快照框应独立于交互页")
	}
}
