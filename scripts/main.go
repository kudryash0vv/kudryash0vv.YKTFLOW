
Copy

package main
 
import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)
 
// --- КОНФИГУРАЦИЯ ---
const (
	OutputFull  = "configs/kudryash0vv_YKTFLOW_1.txt"
	OutputFinal = "configs/kudryash0vv_YKTFLOW_checked.txt"
	SourceFile  = "data/sources_mobile.txt"
	CheckerPath = "checker.py"
)
 
// --- ПАРСИНГ ТЕЛА ОТВЕТА ---
// Источник может отдать:
//   1. Чистый текст с vless:// строками
//   2. Base64-encoded текст (subscription формат)
// Пробуем оба варианта.
 
func extractConfigs(body []byte) []string {
	re := regexp.MustCompile(`(?i)(vless|vmess|trojan|ss)://[^\s"'<>\x00-\x1f]+`)
 
	// Сначала ищем в сыром тексте
	raw := string(body)
	matches := re.FindAllString(raw, -1)
 
	// Пробуем base64 декод (subscription формат)
	trimmed := strings.TrimSpace(raw)
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Пробуем URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(trimmed)
	}
	if err == nil {
		b64matches := re.FindAllString(string(decoded), -1)
		matches = append(matches, b64matches...)
	}
 
	// Убираем дубли внутри одного источника
	seen := make(map[string]struct{})
	var result []string
	for _, m := range matches {
		// Убираем fragment (#name) если есть
		clean := strings.SplitN(m, "#", 2)[0]
		if _, ok := seen[clean]; !ok {
			seen[clean] = struct{}{}
			result = append(result, clean)
		}
	}
	return result
}
 
// --- ГЛАВНАЯ ЛОГИКА ---
 
func process() {
	fmt.Println("🧊 YKTFLOW Engine v9.8 | Инициализация...")
	fmt.Println("📌 Этап 1/2: Сбор конфигов из источников...")
 
	// Читаем sources_mobile.txt
	file, err := os.Open(SourceFile)
	if err != nil {
		fmt.Printf("❌ Ошибка: не найден файл %s\n", SourceFile)
		return
	}
	defer file.Close()
 
	var sources []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Пропускаем пустые строки и комментарии
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "http") {
			sources = append(sources, line)
		}
	}
 
	fmt.Printf("📂 Источников найдено: %d\n", len(sources))
 
	// Для каждого источника — качаем и парсим
	httpClient := &http.Client{Timeout: 15 * time.Second}
	uniqueMap := make(map[string]struct{})
 
	for i, src := range sources {
		fmt.Printf("  [%d/%d] %s\n", i+1, len(sources), src)
 
		resp, err := httpClient.Get(src)
		if err != nil {
			fmt.Printf("    ❌ Ошибка загрузки: %v\n", err)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			fmt.Printf("    ❌ Ошибка чтения: %v\n", err)
			continue
		}
 
		configs := extractConfigs(body)
		for _, c := range configs {
			uniqueMap[c] = struct{}{}
		}
		fmt.Printf("    ✅ Найдено конфигов: %d (всего уникальных: %d)\n", len(configs), len(uniqueMap))
	}
 
	if len(uniqueMap) == 0 {
		fmt.Println("❌ Конфиги не найдены! Проверь sources_mobile.txt")
		return
	}
 
	// Сохраняем все конфиги без фильтрации — checker.py сам разберётся
	saveRaw(OutputFull, uniqueMap)
	fmt.Printf("\n✅ Этап 1 завершён: сохранено %d конфигов → %s\n\n", len(uniqueMap), OutputFull)
 
	// Запускаем checker.py
	runChecker()
}
 
func saveRaw(path string, configs map[string]struct{}) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("❌ Не удалось создать файл: %v\n", err)
		return
	}
	defer f.Close()
 
	fmt.Fprintln(f, "# profile-title: kudryash0vv.YKTFLOW")
	fmt.Fprintln(f, "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200")
	fmt.Fprintf(f, "# update: %s (YKT)\n", time.Now().Format("2006-01-02 / 15:04"))
	fmt.Fprintln(f, "# support: https://github.com")
	fmt.Fprintln(f, "")
 
	for raw := range configs {
		fmt.Fprintln(f, raw)
	}
}
 
func runChecker() {
	fmt.Println("📌 Этап 2/2: Реальная проверка через sing-box (checker.py)...")
 
	if _, err := os.Stat(CheckerPath); os.IsNotExist(err) {
		fmt.Printf("⚠️  checker.py не найден: %s\n", CheckerPath)
		return
	}
 
	if _, err := exec.LookPath("sing-box"); err != nil {
		fmt.Println("⚠️  sing-box не найден в PATH")
		fmt.Println("   Установи: https://github.com/SagerNet/sing-box/releases")
		return
	}
 
	cmd := exec.Command("python3", CheckerPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
 
	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ checker.py завершился с ошибкой: %v\n", err)
		return
	}
 
	fmt.Println("\n🎉 Все этапы завершены!")
	fmt.Printf("📁 Финальные рабочие конфиги: %s\n", OutputFinal)
}
 
func main() {
	os.MkdirAll("configs", 0755)
	os.MkdirAll("data", 0755)
	process()
}
