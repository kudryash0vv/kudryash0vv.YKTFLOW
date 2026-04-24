package main

import (
	"bufio"
	"crypto/tls"
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
	if err != nil { 
		fmt.Printf("Ошибка: не удалось открыть файл %s\n", inputPath)
		return nil 
	}
	defer file.Close()

	var sourceUrls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") { sourceUrls = append(sourceUrls, line) }
	}

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueRaw := make(map[string]struct{})
	client := &http.Client{Timeout: 15 * time.Second}

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
	sem := make(chan struct{}, 100)

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(c, 3*time.Second) // 3 секунды на проверку
			<-sem
			if ping > 0 { resultsChan <- Result{Raw: c, Ping: ping} }
		}(conf)
	}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool { return final[i].Ping < final[j].Ping })
	return final
}

func main() {
	os.MkdirAll("configs", os.ModePerm)

	// Берем только мобильные источники
	fmt.Println("Начинаю сбор мобильных конфигов...")
	mobileNodes := process("data/sources_mobile.txt")
	
	// Выплевываем результат в папку configs
	save("configs/kudryash0vv_YKTFLOW_mobile.txt", mobileNodes)
	fmt.Printf("Готово! Сохранено %d рабочих узлов.\n", len(mobileNodes))
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()
	for _, r := range res { fmt.Fprintln(f, r.Raw) }
}
