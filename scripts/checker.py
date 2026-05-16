import subprocess
import requests
import json
import os
import time
import tempfile
import re
import threading
from urllib.parse import urlparse, parse_qs
from concurrent.futures import ThreadPoolExecutor, as_completed

# --- КОНФИГУРАЦИЯ ---
INPUT_FILE   = "configs/kudryash0vv_YKTFLOW_1.txt"        # файл от main.go
OUTPUT_FILE  = "configs/kudryash0vv_YKTFLOW_checked.txt"  # финальный файл
MAX_CONFIGS  = 200       # сколько рабочих конфигов собрать
THREADS      = 10        # параллельных проверок (не больше 15)
TIMEOUT      = 10        # секунд на проверку одного конфига
TEST_URL     = "https://www.gstatic.com/generate_204"     # лёгкий URL (204 = OK)
BASE_PORT    = 20000     # начальный порт для socks5
SINGBOX_BIN  = "sing-box"  # путь к бинарнику

# --- ГЛОБАЛЬНЫЕ ---
port_lock    = threading.Lock()
used_ports   = set()
results_lock = threading.Lock()
working      = []

def get_free_port():
    with port_lock:
        port = BASE_PORT
        while port in used_ports:
            port += 1
        used_ports.add(port)
        return port

def release_port(port):
    with port_lock:
        used_ports.discard(port)

# --- ПАРСЕРЫ URI ---

def parse_vless(uri):
    try:
        u = urlparse(uri)
        params = parse_qs(u.query)
        return {
            "scheme": "vless",
            "uuid": u.username,
            "host": u.hostname,
            "port": int(u.port or 443),
            "flow": params.get("flow", [""])[0],
            "security": params.get("security", ["none"])[0],
            "sni": params.get("sni", [u.hostname])[0],
            "fp": params.get("fp", ["chrome"])[0],
            "pbk": params.get("pbk", [""])[0],
            "sid": params.get("sid", [""])[0],
            "net_type": params.get("type", ["tcp"])[0],
            "path": params.get("path", ["/"])[0],
            "host_header": params.get("host", [u.hostname])[0],
            "raw": uri
        }
    except Exception:
        return None

def parse_vmess(uri):
    try:
        import base64
        b64 = uri[8:]
        b64 += "=" * (-len(b64) % 4)
        data = json.loads(base64.b64decode(b64).decode())
        return {
            "scheme": "vmess",
            "uuid": data.get("id"),
            "host": data.get("add"),
            "port": int(data.get("port", 443)),
            "alter_id": int(data.get("aid", 0)),
            "security": data.get("tls", ""),
            "sni": data.get("sni", data.get("add", "")),
            "net_type": data.get("net", "tcp"),
            "path": data.get("path", "/"),
            "host_header": data.get("host", data.get("add", "")),
            "raw": uri
        }
    except Exception:
        return None

def parse_trojan(uri):
    try:
        u = urlparse(uri)
        params = parse_qs(u.query)
        return {
            "scheme": "trojan",
            "password": u.username,
            "host": u.hostname,
            "port": int(u.port or 443),
            "sni": params.get("sni", [u.hostname])[0],
            "net_type": params.get("type", ["tcp"])[0],
            "path": params.get("path", ["/"])[0],
            "raw": uri
        }
    except Exception:
        return None

def parse_ss(uri):
    try:
        import base64
        u = urlparse(uri)
        userinfo = u.username or ""
        try:
            decoded = base64.b64decode(userinfo + "==").decode()
            method, password = decoded.split(":", 1)
        except Exception:
            method = userinfo
            password = u.password or ""
        return {
            "scheme": "ss",
            "method": method,
            "password": password,
            "host": u.hostname,
            "port": int(u.port or 8388),
            "raw": uri
        }
    except Exception:
        return None

def parse_config(raw):
    raw = raw.strip().split("#")[0]  # убираем fragment
    if raw.startswith("vless://"):
        return parse_vless(raw)
    elif raw.startswith("vmess://"):
        return parse_vmess(raw)
    elif raw.startswith("trojan://"):
        return parse_trojan(raw)
    elif raw.startswith("ss://"):
        return parse_ss(raw)
    return None

# --- ГЕНЕРАТОР SING-BOX CONFIG ---

def make_singbox_config(parsed, socks_port):
    scheme = parsed["scheme"]

    def tls_block(parsed):
        security = parsed.get("security", "")
        if security == "reality":
            return {
                "enabled": True,
                "server_name": parsed.get("sni", ""),
                "reality": {
                    "enabled": True,
                    "public_key": parsed.get("pbk", ""),
                    "short_id": parsed.get("sid", "")
                },
                "utls": {
                    "enabled": True,
                    "fingerprint": parsed.get("fp", "chrome")
                }
            }
        elif security == "tls":
            return {
                "enabled": True,
                "server_name": parsed.get("sni", ""),
                "insecure": True,
                "utls": {
                    "enabled": True,
                    "fingerprint": parsed.get("fp", "chrome")
                }
            }
        return None

    def transport_block(parsed):
        net_type = parsed.get("net_type", "tcp")
        if net_type == "ws":
            return {
                "type": "ws",
                "path": parsed.get("path", "/"),
                "headers": {"Host": parsed.get("host_header", "")}
            }
        elif net_type == "grpc":
            return {
                "type": "grpc",
                "service_name": parsed.get("path", "")
            }
        return None

    if scheme == "vless":
        outbound = {
            "type": "vless",
            "tag": "proxy",
            "server": parsed["host"],
            "server_port": parsed["port"],
            "uuid": parsed["uuid"],
            "flow": parsed.get("flow", "")
        }
        tls = tls_block(parsed)
        if tls:
            outbound["tls"] = tls
        transport = transport_block(parsed)
        if transport:
            outbound["transport"] = transport

    elif scheme == "vmess":
        outbound = {
            "type": "vmess",
            "tag": "proxy",
            "server": parsed["host"],
            "server_port": parsed["port"],
            "uuid": parsed["uuid"],
            "alter_id": parsed.get("alter_id", 0),
            "security": "auto"
        }
        if parsed.get("security") == "tls":
            outbound["tls"] = {
                "enabled": True,
                "server_name": parsed.get("sni", ""),
                "insecure": True
            }
        transport = transport_block(parsed)
        if transport:
            outbound["transport"] = transport

    elif scheme == "trojan":
        outbound = {
            "type": "trojan",
            "tag": "proxy",
            "server": parsed["host"],
            "server_port": parsed["port"],
            "password": parsed["password"],
            "tls": {
                "enabled": True,
                "server_name": parsed.get("sni", parsed["host"]),
                "insecure": True
            }
        }
        transport = transport_block(parsed)
        if transport:
            outbound["transport"] = transport

    elif scheme == "ss":
        outbound = {
            "type": "shadowsocks",
            "tag": "proxy",
            "server": parsed["host"],
            "server_port": parsed["port"],
            "method": parsed["method"],
            "password": parsed["password"]
        }
    else:
        return None

    return {
        "log": {"level": "error"},
        "inbounds": [{
            "type": "socks",
            "tag": "socks-in",
            "listen": "127.0.0.1",
            "listen_port": socks_port,
            "sniff": False
        }],
        "outbounds": [
            outbound,
            {"type": "direct", "tag": "direct"}
        ]
    }

# --- ПРОВЕРКА КОНФИГА ---

def check_config(raw):
    parsed = parse_config(raw)
    if not parsed:
        return None

    port = get_free_port()
    config = make_singbox_config(parsed, port)
    if not config:
        release_port(port)
        return None

    tmp = tempfile.NamedTemporaryFile(
        mode="w", suffix=".json", delete=False
    )
    json.dump(config, tmp, ensure_ascii=False)
    tmp.close()

    proc = None
    try:
        proc = subprocess.Popen(
            [SINGBOX_BIN, "run", "-c", tmp.name],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL
        )
        time.sleep(1.5)  # ждём пока sing-box поднимется

        proxies = {
            "http":  f"socks5h://127.0.0.1:{port}",
            "https": f"socks5h://127.0.0.1:{port}"
        }
        resp = requests.get(TEST_URL, proxies=proxies, timeout=TIMEOUT)

        if resp.status_code in (200, 204):
            return raw  # ✅ РАБОЧИЙ
        return None

    except Exception:
        return None
    finally:
        if proc:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except Exception:
                proc.kill()
        try:
            os.unlink(tmp.name)
        except Exception:
            pass
        release_port(port)

# --- СОХРАНЕНИЕ ---

def save_results(path, nodes):
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write("# profile-title: kudryash0vv.YKTFLOW\n")
        f.write("# subscription-userinfo: upload=0; download=0; total=885837004800; expire=1798675200\n")
        f.write(f"# update: {time.strftime('%Y-%m-%d / %H:%M')} (YKT)\n")
        f.write("# support: https://github.com\n")
        f.write("# checked: REAL sing-box validation ✅\n\n")
        for i, raw in enumerate(nodes, 1):
            raw_clean = raw.split("#")[0]
            host = urlparse(raw_clean).hostname or "unknown"
            f.write(f"{raw_clean}#[{i:03d}] ✅ {host}\n")
    print(f"💾 Сохранено: {path}")

# --- MAIN ---

def main():
    print("🧊 YKTFLOW Checker | sing-box валидатор")
    print(f"📂 Вход: {INPUT_FILE}")
    print(f"🎯 Цель: {MAX_CONFIGS} рабочих конфигов\n")

    # Проверяем sing-box
    try:
        r = subprocess.run([SINGBOX_BIN, "version"],
                           capture_output=True, timeout=5)
        ver = r.stdout.decode().splitlines()[0] if r.stdout else "ok"
        print(f"✅ sing-box: {ver}\n")
    except Exception:
        print("❌ sing-box не найден!")
        print("   Установи: https://github.com/SagerNet/sing-box/releases")
        return

    # Читаем конфиги
    re_cfg = re.compile(r'(vless|vmess|trojan|ss)://[^\s]+')
    configs = []
    if not os.path.exists(INPUT_FILE):
        print(f"❌ Файл не найден: {INPUT_FILE}")
        return

    with open(INPUT_FILE, "r", encoding="utf-8") as f:
        for line in f:
            m = re_cfg.search(line.strip())
            if m:
                configs.append(m.group(0))

    configs = list(set(configs))
    print(f"📋 Конфигов для проверки: {len(configs)}")
    print(f"🔄 Потоков: {THREADS}\n")

    checked = 0
    found = 0
    stop_event = threading.Event()

    with ThreadPoolExecutor(max_workers=THREADS) as executor:
        futures = {executor.submit(check_config, raw): raw for raw in configs}

        for future in as_completed(futures):
            if stop_event.is_set():
                future.cancel()
                continue

            checked += 1
            result = future.result()

            if result:
                found += 1
                with results_lock:
                    working.append(result)
                host = urlparse(result.split("#")[0]).hostname or "?"
                print(f"  ✅ [{found:03d}/{MAX_CONFIGS}] {host}")

            if checked % 20 == 0:
                print(f"  🔍 Проверено: {checked}/{len(configs)} | Рабочих: {found}")

            if found >= MAX_CONFIGS:
                print(f"\n🎯 Цель достигнута!")
                stop_event.set()

    final = working[:MAX_CONFIGS]
    save_results(OUTPUT_FILE, final)
    print(f"\n✨ Готово! Рабочих конфигов: {len(final)}/{len(configs)} проверено")

if __name__ == "__main__":
    main()
