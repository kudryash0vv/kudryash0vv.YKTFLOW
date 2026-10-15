import os
import json
import socket
import time
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed

INPUT_FILE = "configs/kudryash0vv_YKTFLOW_1.txt"
OUTPUT_FILE = "configs/kudryash0vv_YKTFLOW_checked.txt"
MAX_WORKING_CONFIGS = 50
TIMEOUT_SEC = 2.5
THREADS = 40

# Реалистичные границы пинга для Якутска (мс)
# Меньше 50 мс — это заглушки провайдеров/локалки/Cloudflare Edge, а не рабочие ноды
MIN_LATENCY = 60
MAX_LATENCY = 400

def parse_host_port(raw_line):
    raw_line = raw_line.strip()
    if not raw_line or raw_line.startswith("#"):
        return None, None
    try:
        clean = raw_line.split("#")[0]
        parsed = urllib.parse.urlparse(clean)
        host = parsed.hostname
        port = parsed.port
        
        if not host and clean.startswith("vmess://"):
            import base64
            b64 = clean[8:]
            b64 += "=" * (-len(b64) % 4)
            data = json.loads(base64.b64decode(b64).decode('utf-8', errors='ignore'))
            host = data.get("add")
            port = int(data.get("port", 443))
            
        if host and port:
            return raw_line, (host, port)
    except Exception:
        pass
    return None, None

def test_node(raw_line):
    raw_line, hp = parse_host_port(raw_line)
    if not hp:
        return None, 9999
    
    host, port = hp
    
    # Замеряем честное TCP-время отклика
    start_time = time.time()
    try:
        sock = socket.create_connection((host, port), timeout=TIMEOUT_SEC)
        sock.close()
        latency = int((time.time() - start_time) * 1000)
    except Exception:
        return None, 9999
    
    # Фильтруем аномалии (слишком быстро = фейк/заглушка провайдера)
    if latency < MIN_LATENCY or latency > MAX_LATENCY:
        return None, 9999
        
    return raw_line, latency

def main():
    print("🧊 [Smart Checker] Фильтрация фейков и отбор честных нод для Якутска...")
    
    if not os.path.exists(INPUT_FILE):
        print(f"❌ Файл не найден: {INPUT_FILE}")
        return

    with open(INPUT_FILE, "r", encoding="utf-8") as f:
        lines = f.readlines()

    print(f"📊 Всего кандидатов: {len(lines)}")
    print(f"🎯 Фильтр пинга: от {MIN_LATENCY}ms до {MAX_LATENCY}ms (отсекаем локальные заглушки и мертвяки)...")

    working_nodes = []

    with ThreadPoolExecutor(max_workers=THREADS) as executor:
        futures = {executor.submit(test_node, line): line for line in lines}
        
        for future in as_completed(futures):
            cfg, latency = future.result()
            if cfg:
                working_nodes.append((cfg, latency))

    # Сортируем по стабильному среднему пингу
    working_nodes.sort(key=lambda x: x[1])
    
    # Берем топ-50
    best_nodes = working_nodes[:MAX_WORKING_CONFIGS]

    print(f"✅ Успешно прошло отбор: {len(best_nodes)} конфигураций")

    ykt = time.strftime("%Y-%m-%d / %H:%M", time.gmtime(time.time() + 9*3600))
    
    os.makedirs(os.path.dirname(OUTPUT_FILE), exist_ok=True)
    with open(OUTPUT_FILE, "w", encoding="utf-8") as out:
        out.write("# profile-title: kudryash0vv.YKTFLOW.YAKUTSK\n")
        out.write("# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1801267200\n")
        out.write(f"# update: {ykt} (YKT)\n")
        out.write("# support: https://github.com/kudryash0vv/kudryash0vv.YKTFLOW\n\n")
        
        for cfg, latency in best_nodes:
            out.write(cfg + f"#[Ping: {latency}ms]\n")

    print(f"🎉 Готово! Топ-{len(best_nodes)} реальных узлов записано в {OUTPUT_FILE}")

if __name__ == "__main__":
    main()
