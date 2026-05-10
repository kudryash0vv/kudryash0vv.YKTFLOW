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
	IP      string
}

func isAlive(rawConfig string) (int64, string, bool) {
	u, err := url.Parse(rawConfig)
	if err != nil || u.Hostname() == "" { return 0, "", false }

	host := u.Hostname()
	port := u.Port()
	if port == "" { port = "443" }
	addr := net.JoinHostPort(host, port)

	start := time.Now()
	// TCP соединение
	conn, err := net.DialTimeout("tcp", addr, 2500*time.Millisecond)
	if err != nil { return 0, "", false }
	defer conn.Close()

	// TLS проверка для VLESS/VMess/Trojan
	if strings.Contains(rawConfig, "security=tls") || strings.Contains(rawConfig, "security=reality") {
		sni := u.Query().Get("sni")
		if sni == "" { sni = host }
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
		tlsConn.SetDeadline(time.Now().Add(2000 * time.Millisecond))
		if err := tlsConn.Handshake(); err != nil {
			return 0, "", false
		}
	}

	ping := time.Since(start).Milliseconds()

	// ГЛАВНЫЙ ФИЛЬТР: Пинг строго от 200 до 2000
	if ping < 200 || ping > 2000 {
		return 0, "", false
	}

	return ping, host, true
}

func getCountry(ip string) string {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://ip-api.com" + ip + "?fields=countryCode")
	if err != nil { return "UN" }
	defer resp.Body.Close()
	var geo struct{ CountryCode string `json:"countryCode"` }
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.CountryCode == "" { return "UN" }
	return geo.CountryCode
}

func getFlag(code string) string {
	if len(code) != 2 { return "🌍" }
	r1 := rune(int32(code[0]) + 127397)
	r2 := rune(int32(code[1]) + 127397)
	return string(r1) + string(r2)
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
		for _, link := range re.FindAllString(string(body), -1) {
			uniqueRaw[link] = struct{}{}
		}
	}

	fmt.Printf("🎯 Начинаю отбор элитных конфигов (Пинг: 200-2000мс)...\n")

	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 80)
	var mu sync.Mutex
	usedIPs := make(map[string]bool)

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ping, ip, ok := isAlive(c)
			if ok {
				mu.Lock()
				if usedIPs[ip] {
					mu.Unlock()
					return
				}
				usedIPs[ip] = true
				mu.Unlock()

				code := getCountry(ip)
				resultsChan <- Result{
					Raw:     c,
					Ping:    ping,
					Country: getFlag(code),
					IP:      ip,
				}
			}
		}(conf)
	}

	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	
	// Сортировка по возрастанию пинга
	sort.Slice(final, func(i, j int) bool { return final[i].Ping < final[j].Ping })

	if len(final) > 50 { return final[:50] }
	return final
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()
	fmt.Fprintln(f, "# profile-title: YKTFLOW_STRICT_200_2000")
	fmt.Fprintf(f, "# update: %s | Count: %d\n\n", time.Now().Format("15:04"), len(res))

	for i, r := range res {
		u, _ := url.Parse(r.Raw)
		u.Fragment = fmt.Sprintf("%02d | %s | %dms", i+1, r.Country, r.Ping)
		fmt.Fprintln(f, u.String())
	}
}

func main() {
	os.MkdirAll("configs", os.ModePerm)
	fmt.Println("🚀 YKTFLOW v8.0: Filter 200ms-2000ms")
	
	nodes := process("data/sources_mobile.txt")
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	
	fmt.Printf("✅ Готово! Сохранено %d узлов.\n", len(nodes))
}
