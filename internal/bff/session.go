// Package bff 是 L4 的防腐层：只做三件事——协议翻译（HTTP ↔ Dapr）、
// 会话与权限判定、把事实投影成前端要的形状。
//
// 它不持有任何业务状态：任务在不在、能不能发布、四闸过没过，全部从
// 事实日志重放出来。BFF 重启不丢任何东西，因为没有东西只存在它身上。
//
// 一条硬规则：权限永远来自服务端会话，绝不来自请求体。
// 前端传什么 role / canApprove 一律不采信。
package bff

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// Role 是四个真人角色。
type Role string

const (
	RoleBA Role = "BA"
	RolePA Role = "PA"
	RoleDA Role = "DA"
	RoleSA Role = "SA"
)

// Act 是权限动作名。与原型 ROLES[*].acts 保持一致。
type Act string

const (
	ActBrd         Act = "brd"
	ActBacklog     Act = "backlog"
	ActAcceptAT    Act = "acceptAT"
	ActPbc         Act = "pbc"
	ActStore       Act = "store"
	ActAcceptProto Act = "acceptProto"
	ActBuild       Act = "build"
	ActTest        Act = "test"
	ActDeploy      Act = "deploy"
	ActConfig      Act = "config"
	ActTag         Act = "tag"
	ActView        Act = "view"
)

var roleActs = map[Role][]Act{
	RoleBA: {ActBrd, ActBacklog, ActAcceptAT, ActView},
	RolePA: {ActPbc, ActStore, ActAcceptProto, ActView},
	RoleDA: {ActBuild, ActTest, ActDeploy, ActView},
	RoleSA: {ActConfig, ActTag, ActView},
}

// roleNames 是展示名。
var roleNames = map[Role]string{
	RoleBA: "BA 业务", RolePA: "PA 产品", RoleDA: "DA 交付", RoleSA: "SA 系统",
}

func (r Role) Valid() bool { _, ok := roleActs[r]; return ok }

// Can 判定当前角色能否执行某个动作。唯一的权限入口。
func (r Role) Can(a Act) bool {
	for _, x := range roleActs[r] {
		if x == a {
			return true
		}
	}
	return false
}

// ---------- 会话 ----------

// Sessions 是签名 cookie 会话。存的是角色，不是身份凭据——
// 真接入 SSO 时只需把 role 换成 token 解析结果，Can() 一行不动。
type Sessions struct {
	key []byte
}

func NewSessions(key []byte) *Sessions {
	if len(key) == 0 {
		// 没配密钥就用进程内随机密钥：重启后会话失效，但绝不退化成无签名 cookie。
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	return &Sessions{key: key}
}

const sessionCookie = "i3_sess"

// Issue 写入会话 cookie。
func (s *Sessions) Issue(w http.ResponseWriter, r Role) {
	v := base64.RawURLEncoding.EncodeToString([]byte(r))
	sig := s.sign(v)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    v + "." + sig,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
}

// Get 读会话角色。没有或签名不符时返回默认 BA（只读视角的起点）。
func (s *Sessions) Get(req *http.Request) Role {
	c, err := req.Cookie(sessionCookie)
	if err != nil {
		return RoleBA
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return RoleBA
	}
	if !hmac.Equal([]byte(s.sign(parts[0])), []byte(parts[1])) {
		return RoleBA
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return RoleBA
	}
	r := Role(raw)
	if !r.Valid() {
		return RoleBA
	}
	return r
}

func (s *Sessions) sign(v string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

// ErrForbidden 是权限不足。handler 统一映射成 403。
var ErrForbidden = errors.New("权限不足")

// Require 判定角色能否执行动作。不能则返回 ErrForbidden。
func Require(r Role, a Act) error {
	if !r.Can(a) {
		return ErrForbidden
	}
	return nil
}
