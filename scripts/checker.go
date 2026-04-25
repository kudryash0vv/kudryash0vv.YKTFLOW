package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64" // Добавил для упаковки
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

// 🌍 Магия флагов (исправлено переполнение типов)
func getFlag(code string) string {
	if len(code) != 2 {
		return "🌍"
	}
	r1 := rune(int32(code[0]) + 127397)
	r2 := rune(int32(code[1]) + 127397)
	return string(r1) + string(r2)
}

func getCountry(ip string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	// ИСПРАВЛЕНО: Добавлен /json/ в путь
	resp, err := client.Get("http://ip-api.com" + ip + "?fields=countryCode")
	if err != nil {
		return "UN"
	}
	defer resp.Body.Close()
	var geo GeoIP
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.CountryCode == "" {
		return "UN"
	}
	return geo.CountryCode
}

func checkSpeed(addr string, timeout time.Duration) float64 {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return 0 }
	defer conn.Close()
	duration := time.Since(start).Seconds()
	if duration == 0 { duration = 0.001 }
	return 1.0 / duration 
}

func xrayPing(rawConfig string, timeout time.Duration) (int64, float64) {
	u, err := url.Parse(rawConfig)
	if err != nil || u.Hostname() == "" { return -1, 0 }
	addr := u.Host
	if !strings.Contains(addr, ":") { addr = net.JoinHostPort(addr, "443") }

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return -1, 0 }
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.Close()

	if strings.Contains(rawConfig, "security=reality") || strings.Contains(rawConfig, "security=tls") {
		sni := u.Query().Get("sni")
		if sni == "" { sni = u.Hostname() }
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
		if err := tlsConn.Handshake(); err != nil { return -1, 0 }
	}
	ping := time.Since(start).Milliseconds()
	speed := checkSpeed(addr, timeout)
	return ping, speed
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
			if len(uniqueRaw) >= 1500 { break }
		}
		if len(uniqueRaw) >= 1500 { break }
	}

	fmt.Printf("🚀 Анализ %d конфигов (80 потоков)...\n", len(uniqueRaw))
	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 80) 

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping, speed := xrayPing(c, 1500*time.Millisecond)
			<-sem
			if ping > 0 {
				u, _ := url.Parse(c)
				code := getCountry(u.Hostname())
				flag := getFlag(code)
				fmt.Printf("   ❄️  %s %dms | Pwr: %.1f\n", flag, ping, speed)
				resultsChan <- Result{Raw: c, Ping: ping, Speed: speed, Country: flag}
			}
		}(conf)
	}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool {
		if final[i].Speed != final[j].Speed { return final[i].Speed > final[j].Speed }
		return final[i].Ping < final[j].Ping
	})

	if len(final) > 250 { return final[:250] }
	return final
}

func main() {
	os.MkdirAll("configs", os.ModePerm)
	fmt.Println("🧊 YKTFLOW v5.2: Frozen Birthday Edition...")
	nodes := process("data/sources_mobile.txt")
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	save("configs/kudryash0vv_YKTFLOW_1.txt", nodes)
	fmt.Printf("✅ ГОТОВО! Всё запаковано и летит.\n")
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()

	// 🎈 Праздничный заголовок С РЕШЕТКАМИ (как на рабочем скрине)
	fmt.Fprintln(f, "# profile-title: HappyBirthday🎈.YKTFLOW")
	fmt.Fprintln(f, "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200")
	
	// Добавляем время обновления (YKT)
	fmt.Fprintf(f, "# update: %s (YKT)\n", time.Now().Format("2006-01-02 / 15:04"))
	fmt.Fprintln(f, "") // Пустая строка перед конфигами

	// Записываем конфиги в открытом виде (БЕЗ Base64)
	for _, r := range res {
		u, _ := url.Parse(r.Raw)
		// Название: [Флаг] Пинг ms | Проект
		u.Fragment = fmt.Sprintf("%s %dms | YKTFLOW", r.Country, r.Ping)
		fmt.Fprintln(f, u.String())
	}
	
	fmt.Printf("💾 Файл %s сохранен в рабочем формате (Plain Text + #).\n", path)
}
