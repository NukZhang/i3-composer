// Package artifacts 是一个内容寻址的产物仓库：按 digest 存取构建产物。
//
// 为什么需要它：构建与部署是两个 activity，中间可能隔着进程重启。
// 只把 digest 写进账本不够——部署时得真能把那个字节取回来跑起来。
//
// 布局：<root>/blobs/sha256/<hex>。文件名就是内容摘要，所以
// 「取回来的字节是不是真的那个 digest」可以随时复算核对。
//
// 它是本机文件实现，不是镜像仓库；接真仓库时换掉这一层即可，
// 上层契约（Put/Get/Exists）不变。
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store 是产物仓库。
type Store struct {
	root string
}

// New 造一个仓库。root 为空时用环境变量 ARTIFACT_DIR，
// 再为空就用 ./.i3-artifacts。
func New(root string) *Store {
	if root == "" {
		root = os.Getenv("ARTIFACT_DIR")
	}
	if root == "" {
		root = ".i3-artifacts"
	}
	return &Store{root: root}
}

// Root 是仓库根目录。
func (s *Store) Root() string { return s.root }

// DigestOf 算字节的摘要，格式与账本里一致：sha256:<hex>。
func DigestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Put 存一份产物，按内容寻址。返回 digest 与字节数。
//
// 同一个 digest 存两次是安全的：内容一样，文件一样。
func (s *Store) Put(b []byte) (string, int64, error) {
	d := DigestOf(b)
	hexPart := strings.TrimPrefix(d, "sha256:")
	dir := filepath.Join(s.root, "blobs", "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, hexPart)
	if st, err := os.Stat(path); err == nil {
		return d, st.Size(), nil
	}
	// 并发写同一摘要时，每次写入要有自己的临时路径，否则会覆盖对方尚未发布的字节。
	tmpDir, err := os.MkdirTemp(dir, ".put-*")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(tmpDir)
	tmp := filepath.Join(tmpDir, hexPart)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", 0, err
	}
	return d, int64(len(b)), nil
}

// Get 按 digest 取回产物，并核对内容确实对得上这个 digest。
func (s *Store) Get(digest string) ([]byte, error) {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if hexPart == digest || hexPart == "" {
		return nil, fmt.Errorf("digest 格式非法：%q", digest)
	}
	b, err := os.ReadFile(filepath.Join(s.root, "blobs", "sha256", hexPart))
	if err != nil {
		return nil, fmt.Errorf("产物 %s 不在仓库里：%w", digest, err)
	}
	if got := DigestOf(b); got != digest {
		return nil, fmt.Errorf("产物内容与 digest 不符：期望 %s，实际 %s", digest, got)
	}
	return b, nil
}

// Exists 判定某个 digest 是否在仓库里。
func (s *Store) Exists(digest string) bool {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if hexPart == digest || hexPart == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(s.root, "blobs", "sha256", hexPart))
	return err == nil
}

// List 列出仓库里所有产物的 digest。
func (s *Store) List() ([]string, error) {
	dir := filepath.Join(s.root, "blobs", "sha256")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		out = append(out, "sha256:"+e.Name())
	}
	return out, nil
}
