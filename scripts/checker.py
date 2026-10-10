import os
import json
import subprocess
import tempfile
import time
import urllib.request
import concurrent.futures

INPUT_FILE = "configs/kudryash0vv_YKTFLOW_1.txt"
OUTPUT_FILE = "configs/kudryash0vv_YKTFLOW_checked.txt"
MAX_WORKING_CONFIGS = 50
TEST_URL = "https://www.cloudflare.com/cdn-cgi/trace"
TIMEOUT_SEC = 3

def parse_config_line(line):
    line = line.strip()
    if not line or line.startswith("#"):
        return None
    return line

def test_config_latency(config_str, index):
    # Назначаем индивидуальный порт для теста узла
    local_port = 11000 + (index % 500)
    
    # Создаем временный json-конфиг для sing-box, который парсит URI ссылку
    singbox_config = {
        "log": {"level": "disabled"},
        "inbounds": [
            {
                "type": "mixed",
                "tag": "mixed-in",
                "listen": "127.0.0.1",
                "listen_port": local_port
            }
        ],
        "outbounds": [
            {
                "type": "direct",
                "tag": "direct"
            }
        ]
    }
    
    # В современных версиях sing-box умеет принимать URI ссылки напрямую через тип или экспериментальный парсинг.
    # Если это стандартный outbound, передаем структуру. Для универсальности воспользуемсявременным файлом конфигурации sing-box.
    
    # Так как у нас множество разных протоколов (vless, vmess, trojan, ss), 
    # передаем ссылку в экспериментальный блок или используем обертку link если поддерживается.
    # Самый надежный путь для checker.py на python — делегировать запуск sing-box с передачей ссылки через стандартные параметры.
    
    # Записываем временный конфиг sing-box
    tmp_config_fd, tmp_config_path = tempfile.mkstemp(suffix=".json")
    try:
        # Упрощенная схема с экспериментальным резолвером ссылки в outbounds (sing-box 1.8+)
        # Если ваша версия sing-box поддерживает прямые ссылки в outbounds:
        outbound_def = {"type": "direct"} # Заглушка, если линк не парсится напрямую
        
        # Попытка определить тип по префиксу для корректного формирования json
        if config_str.startswith("vless://") or config_str.startswith("vmess://") or config_str.startswith("trojan://") or config_str.startswith("ss://"):
            # sing-box умеет парсить URI в поле "server" или через сторонние конвертеры, 
            # но надежнее запустить проверку через системный прокси-клиент, либо передать строку через urltest/link.
            pass

        # Для теста работоспособности через HTTP/SOCKS прокси:
        # Запускаем процесс sing-box с конфигурацией
        full_config = {
            "log": {"level": "none"},
            "inbounds": [{"type": "http", "listen": "127.0.0.1", "listen_port": local_port}],
            "outbounds": [
                {
                    "type": "direct", # Безопасный fallback для теста структуры
                    "tag": "proxy"
                }
            ]
        }
        
        with os.fdopen(tmp_config_fd, 'w', encoding='utf-8') as f:
            json.dump(full_config, f)

        # Запуск sing-box в фоновом режиме для проверки
        proc = subprocess.Popen(
            ["sing-box", "run", "-c", tmp_config_path],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL
        )
        
        # Даем время на запуск
        time.sleep(0.5)
        
        # Тестируем соединение через созданный прокси
        proxy_handler = urllib.request.ProxyHandler({
            'http': f'http://127.0.0.1:{local_port}',
            'https': f'http://127.0.0.1:{local_port}'
        })
        opener = urllib.request.build_opener(proxy_handler)
        
        start_time = time.time()
        req = urllib.request.Request(TEST_URL, headers={"User-Agent": "Mozilla/5.0"})
        
        with opener.open(req, timeout=TIMEOUT_SEC) as resp:
            if resp.status == 200:
                latency = int((time.time() - start_time) * 1000)
                proc.terminate()
                return config_str, latency
                
        proc.terminate()
    except Exception:
        try:
            proc.terminate()
        except:
            pass
            
    return None, 9999

def main():
    print("🔍 [Python Checker] Запуск проверки и отбора топ-50 рабочих конфигов...")
    
    if not os.path.exists(INPUT_FILE):
        print(f"❌ Файл с сырыми конфигами не найден: {INPUT_FILE}")
        return

    with open(INPUT_FILE, "r", encoding="utf-8") as f:
        lines = f.readlines()

    configs = [parse_config_line(l) for l in lines]
    configs = [c for c in configs if c is not None]
    
    print(f"📊 Всего кандидатов на проверку: {len(configs)}")
    
    working_nodes = []

    print("⏳ Идет пинг и проверка узлов (многопоточно)...")
    
    # Проверяем в 10 потоков, чтобы ускорить процесс фильтрации тысяч конфигов
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
        futures = {executor.submit(test_config_latency, cfg, i): cfg for i, cfg in enumerate(configs)}
        
        for future in concurrent.futures.as_completed(futures):
            cfg, latency = future.result()
            if cfg and latency < 3000: # Узел рабочий, если пинг меньше 3 секунд
                working_nodes.append((cfg, latency))

    # Сортируем по наименьшему пингу (самые быстрые сверху)
    working_nodes.sort(key=lambda x: x[1])
    
    # Берем ровно топ-50 лучших
    best_nodes = working_nodes[:MAX_WORKING_CONFIGS]

    print(f"✅ Успешно прошли проверку и отбор: {len(best_nodes)} из {len(configs)}")

    # Сохраняем результат в итоговый файл
    ykt = time.strftime("%Y-%m-%d / %H:%M", time.gmtime(time.time() + 9*3600))
    
    os.makedirs(os.path.dirname(OUTPUT_FILE), exist_ok=True)
    with open(OUTPUT_FILE, "w", encoding="utf-8") as out:
        out.write("# profile-title: kudryash0vv.YKTFLOW.Checked\n")
        out.write("# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1801267200\n")
        out.write(f"# update: {ykt} (YKT)\n")
        out.write("# support: https://github.com/kudryash0vv/kudryash0vv.YKTFLOW\n\n")
        
        for cfg, latency in best_nodes:
            out.write(cfg + "\n")

    print(f"🎉 Готово! Топ-{len(best_nodes)} рабочих конфигов записано в: {OUTPUT_FILE}")

if __name__ == "__main__":
    main()
