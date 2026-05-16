package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- КОНФИГУРАЦИЯ ---
const (
	MaxNodes     = 500  // Собираем больше — checker.py отфильтрует до 200 рабочих
	MinPing      = 0
	MaxPing      = 5000
	Threads      = 100
	OutputMobile = "configs/kudryash0vv_YKTFLOW_mobile.txt"
	OutputFull   = "configs/kudryash0vv_YKTFLOW_1.txt"
	OutputFinal  = "configs/kudryash0vv_YKTFLOW_checked.txt" // финальный от checker.py
	SourceFile   = "data/sources_mobile.txt"
	CheckerPath  = "checker.py" // путь к checker.py
)

type Result struct {
	Raw     string
	Ping    int64
	Country string
	IP      string
	Flag    string
}

type GeoIP struct {
	CountryCode string `json:"countryCode"`
}

// --- УТИЛИТЫ ---

func getFlag(code string) string {
	if len(code) != 2 {
		return "🌍"
	}
	code = strings.ToUpper(code)
	return string(rune(code[0])+127397) + string(rune(code[1])+127397)
}

func fetchCountry(ip string) (string, string) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?fields=countryCode")
	if err != nil {
		return "UN", "🌍"
	}
	defer resp.Body.Close()

	var g GeoIP
	json.NewDecoder(resp.Body).Decode(&g)
	if g.CountryCode == "" {
		return "UN", "🌍"
	}
	return g.CountryCode, getFlag(g.CountryCode)
}

// --- ЯДРО ПРОВЕРКИ (быстрый TCP/TLS преотбор) ---

func checkNode(raw string) (*Result, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	target := net.JoinHostPort(host, port)

	start := time.Now()

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, false
	}
	defer conn.Close()

	security := u.Query().Get("security")
	if security == "tls" || security == "reality" {
		sni := u.Query().Get("sni")
		if sni == "" {
			sni = host
		}
		tlsConn := tls.Client(conn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
		})
		tlsConn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			return nil, false
		}

		httpReq := "GET / HTTP/1.1\r\nHost: " + sni + "\r\nConnection: close\r\n\r\n"
		tlsConn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		if _, err := tlsConn.Write([]byte(httpReq)); err != nil {
			return nil, false
		}

		tlsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 64)
		n, err := tlsConn.Read(buf)
		if err != nil || n == 0 {
			return nil, false
		}
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 1)
		conn.Read(buf)
	}

	ping := time.Since(start).Milliseconds()
	if ping > MaxPing {
		return nil, false
	}

	countryCode, flag := fetchCountry(host)

	return &Result{
		Raw:     raw,
		Ping:    ping,
		Country: countryCode,
		IP:      host,
		Flag:    flag,
	}, true
}

// --- ОБРАБОТКА ДАННЫХ ---

func process() {
	fmt.Println("🧊 YKTFLOW Engine v9.7 | Инициализация...")
	fmt.Println("📌 Этап 1/2: Сбор и TCP/TLS преотбор...")

	file, err := os.Open(SourceFile)
	if err != nil {
		fmt.Println("❌ Ошибка: Создайте файл data/sources_mobile.txt")
		return
	}
	defer file.Close()

	var sources []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") {
			sources = append(sources, line)
		}
	}

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueMap := make(map[string]struct{})
	httpClient := &http.Client{Timeout: 15 * time.Second}

	fmt.Printf("📂 Загрузка из %d источников...\n", len(sources))
	for _, urlLink := range sources {
		resp, err := httpClient.Get(urlLink)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		matches := re.FindAllString(string(body), -1)
		for _, m := range matches {
			uniqueMap[m] = struct{}{}
		}
	}

	fmt.Printf("🚀 Анализ %d уникальных конфигов...\n", len(uniqueMap))
	var wg sync.WaitGroup
	resChan := make(chan *Result, len(uniqueMap))
	sem := make(chan struct{}, Threads)

	var mu sync.Mutex
	usedIPs := make(map[string]bool)

	for raw := range uniqueMap {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if node, ok := checkNode(r); ok {
				mu.Lock()
				if !usedIPs[node.IP] {
					usedIPs[node.IP] = true
					resChan <- node
				}
				mu.Unlock()
			}
		}(raw)
	}

	wg.Wait()
	close(resChan)

	var finalNodes []*Result
	for r := range resChan {
		finalNodes = append(finalNodes, r)
	}

	sort.Slice(finalNodes, func(i, j int) bool {
		return finalNodes[i].Ping < finalNodes[j].Ping
	})

	if len(finalNodes) > MaxNodes {
		finalNodes = finalNodes[:MaxNodes]
	}

	// Сохраняем сырые конфиги для checker.py
	saveResults(OutputMobile, finalNodes)
	saveResults(OutputFull, finalNodes)

	fmt.Printf("✅ Этап 1 завершён: собрано %d кандидатов\n\n", len(finalNodes))

	// --- ЭТАП 2: Запуск checker.py ---
	runChecker()
}

func runChecker() {
	fmt.Println("📌 Этап 2/2: Реальная проверка через sing-box (checker.py)...")

	// Проверяем что checker.py существует
	if _, err := os.Stat(CheckerPath); os.IsNotExist(err) {
		fmt.Printf("⚠️  checker.py не найден по пути: %s\n", CheckerPath)
		fmt.Println("   Скачай checker.py и положи рядом с main.go")
		return
	}

	// Проверяем что sing-box установлен
	if _, err := exec.LookPath("sing-box"); err != nil {
		fmt.Println("⚠️  sing-box не найден в PATH")
		fmt.Println("   Установи: https://github.com/SagerNet/sing-box/releases")
		fmt.Println("   Или в GitHub Actions добавь шаг установки sing-box")
		return
	}

	// Запускаем checker.py
	cmd := exec.Command("python3", CheckerPath)
	cmd.Stdout = os.Stdout // выводим прогресс прямо в консоль
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ checker.py завершился с ошибкой: %v\n", err)
		return
	}

	fmt.Println("\n🎉 Все этапы завершены!")
	fmt.Printf("📁 Финальные рабочие конфиги: %s\n", OutputFinal)
}

func saveResults(path string, nodes []*Result) {
	f, _ := os.Create(path)
	defer f.Close()

	fmt.Fprintln(f, "# profile-title: kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200")
	fmt.Fprintf(f, "# update: %s (YKT)\n", time.Now().Format("2006-01-02 / 15:04"))
	fmt.Fprintln(f, "# support: https://github.com")
	fmt.Fprintln(f, "")

	for i, n := range nodes {
		u, _ := url.Parse(n.Raw)
		u.Fragment = fmt.Sprintf("[%02d] %s | %dms", i+1, n.Flag, n.Ping)
		fmt.Fprintln(f, u.String())
	}
}

func main() {
	os.MkdirAll("configs", 0755)
	os.MkdirAll("data", 0755)

	process()
}
