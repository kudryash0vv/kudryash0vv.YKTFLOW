import os
import json
import socket
import time
import requests
import re
import threading
from concurrent.futures import ThreadPoolExecutor

# --- CONFIG YKTFLOW ---
SOURCES_FILE = "data/sources_mobile.txt"
OUTPUT_FILES = ["configs/kudryash0vv_YKTFLOW_mobile.txt", "configs/kudryash0vv_YKTFLOW_1.txt"]
CHECK_URL = "http://google.com"
MIN_PING = 200
MAX_PING = 2000
MAX_NODES = 200
WORKERS = 50

class YKTFlowSubscription:
    def __init__(self):
        self.nodes = []
        self._lock = threading.Lock()

    def get_flag(self, code):
        if not code or len(code) != 2: return "🌍"
        return "".join(chr(ord(c) + 127397) for c in code.upper())

    def get_geo(self, ip):
        try:
            r = requests.get(f"http://ip-api.com{ip}?fields=countryCode", timeout=2).json()
            code = r.get("countryCode", "UN")
            return code, self.get_flag(code)
        except:
            return "UN", "🌍"

    def check_node(self, raw_url):
        try:
            # Парсим хост
            host_match = re.search(r'@([^:/?#]+)', raw_url)
            if not host_match: return
            host = host_match.group(1)

            start = time.time()
            # TCP Check
            with socket.create_connection((host, 443), timeout=2.5):
                ping = int((time.time() - start) * 1000)

            if MIN_PING <= ping <= MAX_PING:
                code, flag = self.get_geo(host)
                with self._lock:
                    self.nodes.append({
                        "raw": raw_url,
                        "ping": ping,
                        "flag": flag,
                        "code": code
                    })
        except:
            pass

    def fetch_sources(self):
        links = []
        if not os.path.exists(SOURCES_FILE): return []
        
        with open(SOURCES_FILE, "r") as f:
            srcs = [l.strip() for l in f if l.startswith("http")]

        for s in srcs:
            try:
                res = requests.get(s, timeout=10).text
                found = re.findall(r'(?:vless|vmess|trojan|ss)://[^\s"\'<>]+', res)
                links.extend(found)
            except: continue
        return list(set(links))

    def generate_subscription(self):
        self.nodes.sort(key=lambda x: x['ping'])
        final = self.nodes[:MAX_NODES]

        header = [
            "# profile-title: kudryash0vv.YKTFLOW",
            "# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200",
            f"# update: {time.strftime('%Y-%m-%d / %H:%M')} (YKT)",
            f"# nodes: {len(final)}",
            "" # Пустая строка после заголовков
        ]

        content = "\n".join(header) + "\n"
        for n in final:
            # Очищаем старый фрагмент и ставим свой: [Флаг] Пинг
            base = n['raw'].split('#')[0]
            name = f"{n['flag']} {n['code']} | {n['ping']}ms"
            content += f"{base}#{name}\n"

        for path in OUTPUT_FILES:
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                f.write(content)

if __name__ == "__main__":
    sub = YKTFlowSubscription()
    raw_links = sub.fetch_sources()
    print(f"🚀 Проверка {len(raw_links)} ссылок...")
    
    with ThreadPoolExecutor(max_workers=WORKERS) as executor:
        executor.map(sub.check_node, raw_links)

    sub.generate_subscription()
    print(f"✅ Подписка обновлена: {len(sub.nodes[:MAX_NODES])} узлов.")
