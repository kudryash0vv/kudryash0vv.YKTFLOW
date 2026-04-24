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

type GeoIP struct {
	CountryCode string `json:"countryCode"`
}

// 🛰 АВТОПОИСК: Ищем новые RAW ссылки на GitHub
func autoDiscover() []string {
	fmt.Println("🔎 Запуск автопоиска новых источников...")
	// Список надежных репозиториев-агрегаторов
	searchUrls := []string{
		"https://githubusercontent.com",
		"https://githubusercontent.com",
		"https://githubusercontent.com",
	}
	return searchUrls
}

func getCountry(ip string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://ip-api.com" + ip + "?fields=countryCode")
	if err != nil { return "🏳️" }
	defer resp.Body.Close()
	var geo GeoIP
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.CountryCode == "" { return "🌍" }
	return geo.CountryCode
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
	// Читаем твои файлы
	file, _ := os.Open(inputPath)
	var sourceUrls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") { sourceUrls = append(sourceUrls) }
	}
	if file != nil { file.Close() }

	// ➕ Добавляем автопоиск к твоим ссылкам
	sourceUrls = append(sourceUrls, autoDiscover()...)

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueRaw := make(map[string]struct{})
	client := &http.Client{Timeout: 15 * time.Second}

	for _, u := range sourceUrls {
		fmt.Printf("🌐 Всасываю: %s\n", u)
		resp, err := client.Get(u)
		if err != nil { continue }
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		found := re.FindAllString(string(body), -1)
		for _, link := range found { 
			uniqueRaw[link] = struct{}{} 
			if len(uniqueRaw) >= 2000 { break } 
		}
		if len(uniqueRaw) >= 2000 { break }
	}

	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 100) 

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(c, 1200*time.Millisecond)
			<-sem
			if ping > 0 { 
				u, _ := url.Parse(c)
				country := getCountry(u.Hostname())
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
	fmt.Println("🧊 YKTFLOW v4.0 (AUTOPILOT): Запуск...")
	nodes := process("data/sources_mobile.txt")
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	save("configs/kudryash0vv_YKTFLOW_1.txt", nodes)
	fmt.Printf("✅ ГОТОВО! Заморожено: %d\n", len(nodes))
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
		u, _ := url.Parse(r.Raw)
		u.Fragment = fmt.Sprintf("[%s] YKTFLOW-%dms", r.Country, r.Ping)
		fmt.Fprintln(f, u.String())
	}
}
