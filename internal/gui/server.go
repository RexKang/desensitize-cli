// Package gui 提供本机网页界面：只监听 127.0.0.1，用一次性 token 拦住别的进程与网页。
//
// 设计取向是「简单」：
//   - 不起额外进程，服务跑在当前进程里，链路是「本进程 → 浏览器」
//   - 随机端口 + 随机 token，绑 127.0.0.1，别的程序扫不到也用不了
//   - 网页每 4 秒发一次心跳，页面关掉 12 秒后进程自己退出（--keep-alive 可关掉）
package gui

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed page.html
var pageFS embed.FS

// Options 是启动参数。
type Options struct {
	RulesPath string // rules.json 路径
	Port      int    // 0 = 随机
	Token     string // 空 = 随机生成
	KeepAlive bool   // 不因为页面关闭而退出
	Version   string
}

// Server 是本机界面服务。
type Server struct {
	opts  Options
	token string
	ln    net.Listener
	srv   *http.Server

	lastPing  atomic.Int64 // 毫秒时间戳
	rulesPath atomic.Value // string：当前规则文件路径（另存为会换掉它）
	once      sync.Once
	done      chan struct{}
	serveErr  chan error
}

// New 建服务但不监听；端口在 Start 里才确定。
func New(opts Options) (*Server, error) {
	token := opts.Token
	if token == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("生成 token 失败: %w", err)
		}
		token = hex.EncodeToString(buf)
	}
	s := &Server{opts: opts, token: token, done: make(chan struct{}), serveErr: make(chan error, 1)}
	s.lastPing.Store(time.Now().UnixMilli())
	s.rulesPath.Store(opts.RulesPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/state", s.auth(s.handleState))
	mux.HandleFunc("/api/preview", s.auth(s.handlePreview))
	mux.HandleFunc("/api/run", s.auth(s.handleRun))
	mux.HandleFunc("/api/rules", s.auth(s.handleRules))
	mux.HandleFunc("/api/ping", s.auth(s.handlePing))
	mux.HandleFunc("/api/quit", s.auth(s.handleQuit))
	mux.HandleFunc("/api/ls", s.auth(s.handleLs))

	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// RulesPath 返回当前规则文件路径。
func (s *Server) RulesPath() string {
	if v, ok := s.rulesPath.Load().(string); ok && v != "" {
		return v
	}
	return s.opts.RulesPath
}

// SetRulesPath 另存为之后切换默认保存位置。
func (s *Server) SetRulesPath(p string) { s.rulesPath.Store(p) }

// Start 监听 127.0.0.1 并起后台服务与看门狗。
func (s *Server) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	s.ln = ln
	go func() {
		err := s.srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			s.serveErr <- err
		}
	}()
	go s.watchdog()
	return nil
}

// URL 是带 token 的访问地址。
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?t=%s", s.ln.Addr().String(), s.token)
}

// Addr 返回实际监听地址（端口随机时用它）。
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Token 暴露给需要自己拼 URL 的调用方（测试用）。
func (s *Server) Token() string { return s.token }

// Wait 阻塞到页面消失、用户按 Ctrl+C 或服务出错。
func (s *Server) Wait() error {
	select {
	case <-s.done:
		return nil
	case err := <-s.serveErr:
		return err
	}
}

// Close 停服务，可重复调用。
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		if s.srv != nil {
			err = s.srv.Close()
		}
	})
	return err
}

// watchdog 在页面心跳停了之后退出进程，避免留下一个没人用的服务。
func (s *Server) watchdog() {
	if s.opts.KeepAlive {
		return
	}
	const idle = 12 * time.Second
	grace := time.Now().Add(15 * time.Second) // 页面还在加载时别急着退
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-t.C:
			if now.Before(grace) {
				continue
			}
			if now.UnixMilli()-s.lastPing.Load() > idle.Milliseconds() {
				_ = s.Close()
				return
			}
		}
	}
}

// auth 校验 token：允许 X-Token 头或 ?t= 查询参数。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Token")
		if got == "" {
			got = r.URL.Query().Get("t")
		}
		if got != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("t") != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := pageFS.ReadFile("page.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
