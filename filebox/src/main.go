package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

// 配置结构体
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Storage struct {
		FilesRoot string `yaml:"files_root"`
		PageDir   string `yaml:"page_dir"`
	} `yaml:"storage"`
	Upload struct {
		MaxMemory   string `yaml:"max_memory"`
		MaxFileSize string `yaml:"max_file_size"`
	} `yaml:"upload"`
}

// 全局配置变量
var config Config

// 从字符串解析大小（支持 K, M, G 后缀）
func parseSize(sizeStr string) (int64, error) {
	sizeStr = strings.TrimSpace(sizeStr)
	if sizeStr == "" {
		return 0, nil
	}

	// 如果没有单位，直接解析为字节
	if num, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
		return num, nil
	}

	// 解析带单位的字符串
	var multiplier int64 = 1
	var numberPart string

	if strings.HasSuffix(sizeStr, "K") || strings.HasSuffix(sizeStr, "k") {
		multiplier = 1024
		numberPart = strings.TrimSuffix(strings.TrimSuffix(sizeStr, "K"), "k")
	} else if strings.HasSuffix(sizeStr, "M") || strings.HasSuffix(sizeStr, "m") {
		multiplier = 1024 * 1024
		numberPart = strings.TrimSuffix(strings.TrimSuffix(sizeStr, "M"), "m")
	} else if strings.HasSuffix(sizeStr, "G") || strings.HasSuffix(sizeStr, "g") {
		multiplier = 1024 * 1024 * 1024
		numberPart = strings.TrimSuffix(strings.TrimSuffix(sizeStr, "G"), "g")
	} else {
		numberPart = sizeStr
	}

	num, err := strconv.ParseFloat(numberPart, 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析大小: %s", sizeStr)
	}

	return int64(num * float64(multiplier)), nil
}

// 加载配置文件
func loadConfig(configPath string) error {
	// 获取可执行文件路径
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取可执行文件路径: %v", err)
	}
	exeDir := filepath.Dir(exe)

	var configPathFinal string
	if configPath != "" {
		if !filepath.IsAbs(configPath) {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("无法获取当前工作目录: %v", err)
			}
			configPathFinal = filepath.Join(cwd, configPath)
		} else {
			configPathFinal = configPath
		}
	} else {
		configPathFinal = filepath.Join(exeDir, "..", "filebox.yml")
	}

	// 检查配置文件是否存在
	if _, err := os.Stat(configPathFinal); os.IsNotExist(err) {
		log.Printf("配置文件不存在，使用默认配置: %s", configPathFinal)
		setDefaultConfig(exeDir)
		return nil
	}

	// 读取配置文件
	data, err := os.ReadFile(configPathFinal)
	if err != nil {
		return fmt.Errorf("无法读取配置文件: %v", err)
	}

	// 解析 YAML
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("无法解析配置文件: %v", err)
	}

	// 设置默认值（如果配置为空）
	setDefaultConfig(exeDir)

	log.Printf("已加载配置文件: %s", configPathFinal)
	return nil
}

// 设置默认配置
func setDefaultConfig(exeDir string) {
	// 服务器端口
	if config.Server.Port == 0 {
		config.Server.Port = 8080
	}

	// 文件根目录
	if config.Storage.FilesRoot == "" {
		config.Storage.FilesRoot = filepath.Join(exeDir, "..", "files")
	}

	// 页面目录
	if config.Storage.PageDir == "" {
		config.Storage.PageDir = filepath.Join(exeDir, "..", "page")
	}

	// 上传内存限制
	if config.Upload.MaxMemory == "" {
		config.Upload.MaxMemory = "10M"
	}

	// 上传文件总大小限制（默认不限制）
	if config.Upload.MaxFileSize == "" {
		config.Upload.MaxFileSize = "0"
	}
}

var (
	filesRoot   string // 实际存储文件的目录
	pageDir     string // 前端静态页面目录
	maxMemory   int64  // 上传内存限制（字节）
	maxFileSize int64  // 上传文件总大小限制（字节）
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ---------- 文件信息结构 ----------
type FileInfo struct {
	Name    string    `json:"name"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// ---------- 安全路径处理 ----------
func safePath(path string) (string, error) {
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid path")
	}
	full := filepath.Join(filesRoot, clean)
	absRoot, _ := filepath.Abs(filesRoot)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) && absFull != absRoot {
		return "", fmt.Errorf("path outside root")
	}
	return full, nil
}

// ---------- API: 列出目录 ----------
func listHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	full, err := safePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.IsDir() {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var infos []FileInfo
	for _, name := range names {
		childPath := filepath.Join(full, name)
		childStat, err := os.Stat(childPath)
		if err != nil {
			continue
		}
		infos = append(infos, FileInfo{
			Name:    name,
			IsDir:   childStat.IsDir(),
			Size:    childStat.Size(),
			ModTime: childStat.ModTime(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

// ---------- API: 上传文件（返回 JSON + SHA256） ----------
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 限制上传文件总大小（如果配置了）
	if maxFileSize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxFileSize)
	}

	// 解析 multipart 表单
	err := r.ParseMultipartForm(maxMemory)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, fmt.Sprintf("文件大小超过限制 (%s)", config.Upload.MaxFileSize), http.StatusBadRequest)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 再次检查文件大小（因为 ParseMultipartForm 可能不会完全限制）
	if maxFileSize > 0 && header.Size > maxFileSize {
		http.Error(w, fmt.Sprintf("文件大小超过限制 (%s)", config.Upload.MaxFileSize), http.StatusBadRequest)
		return
	}

	targetPath := r.FormValue("path")
	if targetPath == "" {
		targetPath = "."
	}
	cleanPath := filepath.Clean(targetPath)
	if strings.Contains(cleanPath, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	fullDir := filepath.Join(filesRoot, cleanPath)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fullFile := filepath.Join(fullDir, header.Filename)
	absRoot, _ := filepath.Abs(filesRoot)
	absFile, _ := filepath.Abs(fullFile)
	if !strings.HasPrefix(absFile, absRoot+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 创建目标文件，同时计算 SHA256
	dst, err := os.Create(fullFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	hash := sha256.New()
	multiWriter := io.MultiWriter(dst, hash)

	_, err = io.Copy(multiWriter, file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sha256Sum := hex.EncodeToString(hash.Sum(nil))

	// 返回 JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"sha256": sha256Sum,
	})
}

// ---------- API: 下载文件 ----------
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	full, err := safePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 设置响应头，告诉浏览器文件名
    w.Header().Set("Content-Disposition", "filename=\"" + filepath.Base(full) + "\"")
	http.ServeFile(w, r, full)
}

// ---------- WebSocket 文本协作 ----------
type Message struct {
	Text string `json:"text"`
}

var (
	clients     = make(map[*websocket.Conn]bool)
	broadcast   = make(chan Message)
	currentText = ""
	mu          sync.Mutex
)

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print(err)
		return
	}
	defer conn.Close()

	mu.Lock()
	initial := currentText
	mu.Unlock()
	if err := conn.WriteJSON(Message{Text: initial}); err != nil {
		log.Println("write initial:", err)
		return
	}

	clients[conn] = true
	defer func() {
		delete(clients, conn)
	}()

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("read error: %v", err)
			break
		}
		mu.Lock()
		currentText = msg.Text
		mu.Unlock()
		broadcast <- msg
	}
}

func broadcastMessages() {
	for {
		msg := <-broadcast
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				log.Printf("write error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

// ---------- 初始化路径和配置 ----------
func initApp(configPath string) error {
	// 加载配置文件
	if err := loadConfig(configPath); err != nil {
		return err
	}

	// 解析大小配置
	var err error

	// 解析上传内存限制
	maxMemory, err = parseSize(config.Upload.MaxMemory)
	if err != nil {
		return fmt.Errorf("无效的上传内存限制: %v", err)
	}

	// 解析上传文件总大小限制
	maxFileSize, err = parseSize(config.Upload.MaxFileSize)
	if err != nil {
		return fmt.Errorf("无效的上传文件大小限制: %v", err)
	}

	// 获取可执行文件路径
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取可执行文件路径: %v", err)
	}
	exeDir := filepath.Dir(exe)

	// 设置文件根目录
	filesRoot = config.Storage.FilesRoot
	if !filepath.IsAbs(filesRoot) {
		filesRoot = filepath.Join(exeDir, "..", filesRoot)
	}
	filesRoot, _ = filepath.Abs(filesRoot)

	// 设置页面目录
	pageDir = config.Storage.PageDir
	if !filepath.IsAbs(pageDir) {
		pageDir = filepath.Join(exeDir, "..", pageDir)
	}
	pageDir, _ = filepath.Abs(pageDir)

	// 确保目录存在
	if err := os.MkdirAll(filesRoot, 0755); err != nil {
		return fmt.Errorf("无法创建 files 目录: %v", err)
	}
	if _, err := os.Stat(pageDir); os.IsNotExist(err) {
		return fmt.Errorf("page 目录不存在: %s", pageDir)
	}

	// 打印配置信息
	log.Printf("=== FileBox 配置 ===")
	log.Printf("监听端口: %d", config.Server.Port)
	log.Printf("文件存储目录: %s", filesRoot)
	log.Printf("页面目录: %s", pageDir)
	log.Printf("上传内存限制: %s (%d bytes)", config.Upload.MaxMemory, maxMemory)
	if maxFileSize > 0 {
		log.Printf("上传文件总大小限制: %s (%d bytes)", config.Upload.MaxFileSize, maxFileSize)
	} else {
		log.Printf("上传文件总大小限制: 无限制")
	}
	log.Printf("===================")

	return nil
}

// ---------- 主函数 ----------
func main() {
	// 支持命令行参数指定配置文件
	configFile := flag.String("config", "", "配置文件路径")
	flag.Parse()

	// 初始化应用
	if err := initApp(*configFile); err != nil {
		log.Fatal(err)
	}

	// 静态文件服务（前端页面）
	http.Handle("/", http.FileServer(http.Dir(pageDir)))

	// API 路由
	http.HandleFunc("/api/list", listHandler)
	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/download", downloadHandler)
	http.HandleFunc("/ws/editor", handleWebSocket)

	// 启动 WebSocket 广播协程
	go broadcastMessages()

	addr := fmt.Sprintf(":%d", config.Server.Port)
	log.Printf("服务器启动，监听端口 %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
