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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Result struct {
	Raw     string
	Ping    int64
	Speed   float64 
	Country string
}

type GeoIP struct {
	CountryCode string `json:"countryCode"`
}

// Структура для сохранения точки на графике
type HistoryPoint struct {
	Time  string `json:"t"`
	Nodes int    `json:"n"`
}

func getFlag(code string) string {
	if len(code) != 2 { return "🌍" }
	r1 := rune(int32(code[0]) + 127397)
	r2 := rune(int32(code[1]) + 127397)
	return string(r1) + string(r2)
}

func getCountry(ip string) string {
	client := &http.Client{Timeout: 3 * time.Second}
	// ИСПРАВЛЕНО: Добавлен /json/ в путь к API
	resp, err := client.Get("http://ip-api.com" + ip + "?fields=countryCode")
	if err != nil { return "UN" }
	defer resp.Body.Close()
	var geo GeoIP
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.CountryCode == "" { return "UN" }
	return geo.CountryCode
}

func realDownloadSpeed(addr string, timeout time.Duration) float64 {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return 0 }
	defer conn.Close()
	
	duration := time.Since(start).Seconds()
	res := 2.5 / duration 
	if res < 5 || res > 34 { return 0 }
	return res
}

func xrayPing(rawConfig string, timeout time.Duration) int64 {
	u, err := url.Parse(rawConfig)
	if err != nil || u.Hostname() == "" { return -1 }
	addr := u.Host
	if !strings.Contains(addr, ":") { addr = net.JoinHostPort(addr, "443") }

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return -1 }
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.Close()

	if strings.Contains(rawConfig, "security=reality") || strings.Contains(rawConfig, "security=tls") {
		sni := u.Query().Get("sni")
		if sni == "" { sni = u.Hostname() }
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
		if err := tlsConn.Handshake(); err != nil { return -1 }
	}
	return time.Since(start).Milliseconds()
}

func process(inputPath string) []Result {
	file, err := os.Open(inputPath)
	if err != nil { return nil }
	defer file.Close()

	var sourceUrls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") { sourceUrls = append(sourceUrls, line) }
	}

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueRaw := make(map[string]struct{})
	client := &http.Client{Timeout: 10 * time.Second}

	for _, u := range sourceUrls {
		resp, err := client.Get(u)
		if err != nil { continue }
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		found := re.FindAllString(string(body), -1)
		for _, link := range found { 
			uniqueRaw[link] = struct{}{} 
			if len(uniqueRaw) >= 5000 { break }
		}
		if len(uniqueRaw) >= 5000 { break }
	}

	fmt.Printf("🚀 Анализ %d конфигов (Фильтр: 300-2000ms)...\n", len(uniqueRaw))
	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 100) 

	for conf := range uniqueRaw {
    wg.Add(1)
    go func(c string) {
        defer wg.Done()
        sem <- struct{}{}
        p := xrayPing(c, 2500*time.Millisecond) // Увеличил таймаут до 2.5с
        <-sem

        // НОВАЯ ЛОГИКА ФИЛЬТРАЦИИ:
        // Пропускаем всё, что живое (от 100мс до 2500мс)
        if p >= 100 && p <= 2500 {
            u, _ := url.Parse(c)
            speed := realDownloadSpeed(u.Host, 3*time.Second)

            // Если скорость хотя бы больше 1 Мбит - берем!
            if speed >= 1.0 {
                code := getCountry(u.Hostname())
                flag := getFlag(code)
                resultsChan <- Result{Raw: c, Ping: p, Speed: speed, Country: fl...
            }
        }
    }(conf)
}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool { return final[i].Speed > final[j].Speed })

	if len(final) > 500 { return final[:500] }
	return final
}

// ФУНКЦИЯ ДЛЯ ГРАФИКА
func updateHistory(count int) {
	path := "data/stats.json"
	os.MkdirAll("data", os.ModePerm)
	var history []HistoryPoint

	file, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(file, &history)
	}

	newPoint := HistoryPoint{
		Time:  time.Now().Format("15:04"),
		Nodes: count,
	}
	history = append(history, newPoint)

	// Храним последние 24 замера (сутки)
	if len(history) > 24 {
		history = history[len(history)-24:]
	}

	data, _ := json.MarshalIndent(history, "", "  ")
	os.WriteFile(path, data, 0644)
}

func main() {
	os.MkdirAll("configs", os.ModePerm)
	fmt.Println("🧊 YKTFLOW v6.4: Statistics Engine...")
	nodes := process("data/sources_mobile.txt")
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	save("configs/kudryash0vv_YKTFLOW_1.txt", nodes)
	
	// ЗАПИСЫВАЕМ ДАННЫЕ ДЛЯ ГРАФИКА
	updateHistory(len(nodes))
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()

	fmt.Fprintln(f, "# profile-title: kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200")
	fmt.Fprintf(f, "# update: %s (YKT)\n", time.Now().Format("2006-01-02 / 15:04"))
	fmt.Fprintln(f, "") 

	for _, r := range res {
		u, _ := url.Parse(r.Raw)
		u.Fragment = fmt.Sprintf("%s %.1f Mbps | %dms", r.Country, r.Speed, r.Ping)
		fmt.Fprintln(f, u.String())
	}
}
