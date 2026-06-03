package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	outputFull  = "configs/kudryash0vv_YKTFLOW_1.txt"
	outputFinal = "configs/kudryash0vv_YKTFLOW_checked.txt"
	sourceFile  = "data/sources_mobile.txt"
	checkerPath = "scripts/checker.py"
	userAgent   = "YKTFLOW-Engine/10.0"
)

var configRE = regexp.MustCompile(`(?i)(vless|vmess|trojan|ss)://[^\s"'<>\x00-\x1f]+`)

func normalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "github.com") || !strings.Contains(raw, "/blob/") {
		return raw
	}
	// https://github.com/user/repo/blob/branch/path -> raw.githubusercontent.com/user/repo/branch/path
	raw = strings.Replace(raw, "https://github.com/", "https://raw.githubusercontent.com/", 1)
	raw = strings.Replace(raw, "http://github.com/", "https://raw.githubusercontent.com/", 1)
	raw = strings.Replace(raw, "/blob/", "/", 1)
	return raw
}

func decodeBase64Flexible(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	pad := (4 - len(s)%4) % 4
	s += strings.Repeat("=", pad)

	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, true
	}
	return nil, false
}

func extractFromText(text string) []string {
	return configRE.FindAllString(text, -1)
}

func extractConfigs(body []byte) []string {
	raw := string(body)
	seen := make(map[string]struct{})
	add := func(list []string) {
		for _, m := range list {
			clean := strings.SplitN(m, "#", 2)[0]
			clean = strings.TrimSpace(clean)
			if clean == "" {
				continue
			}
			if _, ok := seen[clean]; !ok {
				seen[clean] = struct{}{}
			}
		}
	}

	add(extractFromText(raw))

	// Whole-body base64 subscription.
	if decoded, ok := decodeBase64Flexible(raw); ok {
		add(extractFromText(string(decoded)))
	}

	// Line-by-line base64 (common subscription format).
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "://") {
			add(extractFromText(line))
			continue
		}
		if decoded, ok := decodeBase64Flexible(line); ok {
			add(extractFromText(string(decoded)))
		}
	}

	out := make([]string, 0, len(seen))
	for cfg := range seen {
		out = append(out, cfg)
	}
	sort.Strings(out)
	return out
}

func fetchSource(client *http.Client, src string) ([]byte, error) {
	src = normalizeSourceURL(src)
	req, err := http.NewRequest(http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func loadSources(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 10*1024*1024)

	var sources []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "http") {
			sources = append(sources, line)
		}
	}
	return sources, sc.Err()
}

func process() {
	fmt.Println("🧊 YKTFLOW Engine v10.0 | Инициализация...")
	fmt.Println("📌 Этап 1/2: Сбор конфигов из источников...")

	sources, err := loadSources(sourceFile)
	if err != nil {
		fmt.Printf("❌ Ошибка: не найден файл %s (%v)\n", sourceFile, err)
		return
	}
	fmt.Printf("📂 Источников найдено: %d\n", len(sources))

	client := &http.Client{Timeout: 20 * time.Second}
	unique := make(map[string]struct{})
	errorsCount := 0

	for i, src := range sources {
		fmt.Printf("  [%d/%d] %s\n", i+1, len(sources), src)
		normalized := normalizeSourceURL(src)
		if normalized != src {
			fmt.Printf("    ↪ raw: %s\n", normalized)
		}

		body, err := fetchSource(client, src)
		if err != nil {
			fmt.Printf("    ❌ Ошибка загрузки: %v\n", err)
			errorsCount++
			continue
		}

		configs := extractConfigs(body)
		for _, c := range configs {
			unique[c] = struct{}{}
		}
		fmt.Printf("    ✅ Найдено: %d | уникальных всего: %d\n", len(configs), len(unique))
	}

	if len(unique) == 0 {
		fmt.Printf("❌ Конфиги не найдены! Ошибок загрузки: %d. Проверь %s\n", errorsCount, sourceFile)
		return
	}

	saveRaw(outputFull, unique)
	fmt.Printf("\n✅ Этап 1 завершён: сохранено %d конфигов → %s\n\n", len(unique), outputFull)
	runChecker()
}

func saveRaw(path string, configs map[string]struct{}) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Printf("❌ Не удалось создать каталог: %v\n", err)
		return
	}

	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("❌ Не удалось создать файл: %v\n", err)
		return
	}
	defer f.Close()

	ykt := time.Now().In(time.FixedZone("YKT", 9*3600)).Format("2006-01-02 / 15:04")
	fmt.Fprintln(f, "# profile-title: kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1801267200")
	fmt.Fprintf(f, "# update: %s (YKT)\n", ykt)
	fmt.Fprintln(f, "# support: https://github.com/kudryash0vv/kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "")

	keys := make([]string, 0, len(configs))
	for k := range configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, raw := range keys {
		fmt.Fprintln(f, raw)
	}
}

func pythonBin() string {
	for _, bin := range []string{"python3", "python"} {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return "python3"
}

func runChecker() {
	fmt.Println("📌 Этап 2/2: Реальная проверка через sing-box (checker.py)...")

	if _, err := os.Stat(checkerPath); os.IsNotExist(err) {
		fmt.Printf("⚠️  checker.py не найден: %s\n", checkerPath)
		return
	}
	if _, err := exec.LookPath("sing-box"); err != nil {
		fmt.Println("⚠️  sing-box не найден в PATH")
		fmt.Println("   Установи: https://github.com/SagerNet/sing-box/releases")
		return
	}

	py := pythonBin()
	cmd := exec.Command(py, checkerPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ checker.py завершился с ошибкой: %v\n", err)
		return
	}

	fmt.Println("\n🎉 Все этапы завершены!")
	fmt.Printf("📁 Финальные рабочие конфиги: %s\n", outputFinal)
}

func main() {
	_ = os.MkdirAll("configs", 0755)
	_ = os.MkdirAll("data", 0755)
	process()
}
