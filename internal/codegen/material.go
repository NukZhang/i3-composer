package codegen

import (
	"embed"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed materialinbound/*.go materialinbound/web/*
var materialSource embed.FS

var materialAcceptance = []Acceptance{
	{ID: "AT-01", Name: "入库类型默认值", Given: "选择采购入库或甲供入库", When: "新建入库单", Then: "带出对应上游单据、公司资产或甲方资产及一次性进成本"},
	{ID: "AT-02", Name: "正常入库", Given: "钢材剩余 120.000 吨且采购合同有效", When: "确认 80.400 吨", Then: "生成可查询入库单，余量为 39.600 吨"},
	{ID: "AT-03", Name: "超量阻断", Given: "钢材剩余 120.000 吨", When: "确认 120.001 吨", Then: "提示超计划，余量和入库流水保持原值"},
	{ID: "AT-04", Name: "台账追溯", Given: "已有确认的入库单", When: "按单号查询库房收发存明细", Then: "可核对来源、材料、数量、单位、资产归属和财务口径"},
	{ID: "AT-05", Name: "部署可用", Given: "同一构建已部署", When: "请求 /healthz", Then: "返回 HTTP 200 和当前单元与提交标识"},
}

func materialCode(s Spec) map[string]string {
	// Go 1.22 的 ServeMux 方法和路径参数由生成模块自己的 go 指令启用。
	out := map[string]string{"go.mod": "module i3/" + s.UnitID + "\n\ngo 1.22\n"}
	const prefix = "materialinbound/"
	// 固定的编译期制品目录。业务服务与测试从同一组源文件构建。
	_ = fs.WalkDir(materialSource, "materialinbound", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := materialSource.ReadFile(path)
		if err != nil {
			return err
		}
		file := strings.TrimPrefix(path, prefix)
		text := string(body)
		if file == "main.go" {
			text = strings.Replace(text, `const unitID = "material-inbound"`, "const unitID = "+strconv.Quote(s.UnitID), 1)
		}
		out[file] = text
		return nil
	})
	return out
}
