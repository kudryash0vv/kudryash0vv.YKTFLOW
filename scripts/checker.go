package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
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
	Raw  string
	Ping int64
}

func xrayPing(rawConfig string, timeout time.Duration) int64 {
	u, err := url.Parse(rawConfig)
	if err != nil || u.Hostname() == "" { return -1 }
	addr := u.Host
	if !strings.Contains(addr, ":") { addr = net.JoinHostPort(addr, "443") }

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return -1 }
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
		if strings.HasPrefix(line, "http") { 
			sourceUrls = append(sourceUrls, line) 
			if len(sourceUrls) >= 5 { break }
		}
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
		for _, link := range found { uniqueRaw[link] = struct{}{} }
	}

	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 500)

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(c, 1*time.Second) 
			<-sem
			if ping > 0 { resultsChan <- Result{Raw: c, Ping: ping} }
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

	fmt.Println("❄️ Морозный чекер запущен (Лимит: 5 источников)...")
	nodes := process("data/sources_mobile.txt")
	
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", nodes)
	save("configs/kudryash0vv_YKTFLOW_1.txt", nodes)
	
	fmt.Printf("✅ Готово! Найдено %d быстрых узлов.\n", len(nodes))
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()

	// Формируем заголовок подписки
	// 825 GB в байтах: 825 * 1024 * 1024 * 1024 = 885837004800
	// 31.12.2026 в Unix Timestamp: 1798675200
	header := "profile-title: kudryash0vv.YKTFLOW\n"
	header += "subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200\n\n"

	content := header
	for _, r := range res {
		content += r.Raw + "\n"
	}

	// Кодируем всё в Base64, чтобы клиенты корректно отобразили инфо-панель
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	fmt.Fprintln(f, encoded)
}
