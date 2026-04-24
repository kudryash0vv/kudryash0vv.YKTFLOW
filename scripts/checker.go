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
	Country string
}

// Структура для получения страны по IP
type GeoIP struct {
	CountryCode string `json:"countryCode"`
}

func getCountry(ip string) string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://ip-api.com" + ip + "?fields=countryCode")
	if err != nil {
		return "🏳️"
	}
	defer resp.Body.Close()

	var geo GeoIP
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return "🌍"
	}
	// Превращаем код страны (RU, US) в эмодзи флаг
	if len(geo.CountryCode) != 2 {
		return "🌍"
	}
	return strings.ToUpper(geo.CountryCode)
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
	if err != nil { 
		fmt.Printf("❌ Ошибка: не удалось открыть %s\n", inputPath)
		return nil 
	}
	defer file.Close()

	var sourceUrls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") { 
			sourceUrls = append(sourceUrls, line) 
			if len(sourceUrls) >= 5 { break }
		}
	}
	fmt.Printf("📂 Нашел %d источников в %s\n", len(sourceUrls), inputPath)

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueRaw := make(map[string]struct{})
	client := &http.Client{Timeout: 10 * time.Second}

	for i, u := range sourceUrls {
		fmt.Printf("🌐 [%d/%d] Всасываю: %s...\n", i+1, len(sourceUrls), u)
		resp, err := client.Get(u)
		if err != nil { continue }
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		found := re.FindAllString(string(body), -1)
		for _, link := range found { 
			uniqueRaw[link] = struct{}{} 
			if len(uniqueRaw) >= 1000 { break } // Немного снизил лимит для скорости
		}
		if len(uniqueRaw) >= 1000 { break }
	}

	fmt.Printf("🚀 Проверка %d конфигов (100 потоков)...\n", len(uniqueRaw))
	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 100) 

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(c, 1500*time.Millisecond)
			<-sem
			if ping > 0 { 
				u, _ := url.Parse(c)
				country := getCountry(u.Hostname())
				fmt.Printf("   ❄️  [%s] Живой! [%d ms]\n", country, ping)
				resultsChan <- Result{Raw: c, Ping: ping, Country: country} 
			}
		}(conf)
	}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool { return final[i].Ping < final[j].Ping })

	if len(final) > 200 { return final[:200] }
	return final
}

func main() {
	os.MkdirAll("configs", os.ModePerm)
	fmt.Println("🧊 YKTFLOW v3.0: Запуск морозного комбайна...")
	nodes := process("data/sources_mobile.txt")
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	save("configs/kudryash0vv_YKTFLOW_1.txt", nodes)
	fmt.Printf("✅ ГОТОВО! Заморожено узлов: %d\n", len(nodes))
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()

	fmt.Fprintln(f, "profile-title: kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "subscription-userinfo: upload=0;download=0;total=885837004800;expire=1798675200")
	fmt.Fprintln(f, "subscription-update-interval: 4")
	fmt.Fprintln(f, "support-url: https://github.io")
	fmt.Fprintln(f, "") 

	for _, r := range res {
		// Добавляем страну в название конфига (после # в URL)
		u, err := url.Parse(r.Raw)
		if err == nil {
			originalName := u.Fragment
			if originalName == "" { originalName = "Node" }
			// Формат: [СТРАНА] Название # Ссылка
			newName := fmt.Sprintf("[%s] %s", r.Country, originalName)
			u.Fragment = newName
			fmt.Fprintln(f, u.String())
		} else {
			fmt.Fprintln(f, r.Raw)
		}
	}
}
