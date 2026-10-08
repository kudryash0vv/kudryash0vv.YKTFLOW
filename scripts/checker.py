import os
import re
import json
import time
import tempfile
import threading
import subprocess
import requests
import base64
from urllib.parse import urlparse, parse_qs
from concurrent.futures import ThreadPoolExecutor, as_completed

# --- КОНФИГУРАЦИЯ ---
INPUT_FILE   = "configs/kudryash0vv_YKTFLOW_1.txt"
OUTPUT_FILE  = "configs/kudryash0vv_YKTFLOW_checked.txt"
MAX_CONFIGS  = 200
THREADS      = 12
TIMEOUT      = 7
TEST_URLS    = [
    "https://www.gstatic.com/generate_204",
    "https://cp.cloudflare.com/"
]
BASE_PORT    = 20000
SINGBOX_BIN  = "sing-box"

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

def is_blacklisted_host(host):
    if not host:
        return True
    host = host.lower().strip()
    blacklisted = [
        "127.0.0.1", "localhost", "0.0.0.0", "example.com",
        "your-domain.com", "10.0.0.", "192.168."
    ]
    return any(b in host for b in blacklisted)

# --- ПАРСЕРЫ ---

def parse_vless(uri):
    try:
        u = urlparse(uri)
        if not u.hostname or is_blacklisted_host(u.hostname):
            return None
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
        b64 = uri[8:]
        b64 += "=" * (-len(b64) % 4)
        data = json.loads(base64.b64decode(b64).decode('utf-8', errors='ignore'))
        host = data.get("add")
        if not host or is_blacklisted_host(host):
            return None
        return {
            "scheme": "vmess",
            "uuid": data.get("id"),
            "host": host,
            "port": int(data.get("port", 443)),
            "alter_id": int(data.get("aid", 0)),
            "security": data.get("tls", ""),
            "sni": data.get("sni", host),
            "net_type": data.get("net", "tcp"),
            "path": data.get("path", "/"),
            "host_header": data.get("host", host),
            "raw": uri
        }
    except Exception:
        return None

def parse_trojan(uri):
    try:
        u = urlparse(uri)
        if not u.hostname or is_blacklisted_host(u.hostname):
            return None
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
        u = urlparse(uri)
        if not u.hostname or is_blacklisted_host(u.hostname):
            return None
        userinfo = u.username or ""
        try:
            decoded = base64.b64decode(userinfo + "==").decode('utf-8', errors='ignore')
            method, password = decoded.split(":", 1)
        except Exception:
            method = userinfo
            password = u.password or ""
        if not method or not password:
            return None
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
    raw_clean = raw.strip().split("#")[0]
    if raw_clean.startswith("vless://"):
        return parse_vless(raw_clean)
    elif raw_clean.startswith("vmess://"):
        return parse_vmess(raw_clean)
    elif raw_clean.startswith("trojan://"):
        return parse_trojan(raw_clean)
    elif raw_clean.startswith("ss://"):
        return parse_ss(raw_clean)
    return None

# --- SING-BOX CONFIG BUILDER ---

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
                "insecure": False,
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
                "insecure": False
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
                "insecure": False
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

# --- ВАЛИДАЦИЯ ---

def check_config(raw):
    parsed = parse_config(raw)
    if not parsed:
        return None

    port = get_free_port()
    config = make_singbox_config(parsed, port)
    if not config:
        release_port(port)
        return None

    tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
    json.dump(config, tmp, ensure_ascii=False)
    tmp.close()

    proc = None
    try:
        proc = subprocess.Popen(
            [SINGBOX_BIN, "run", "-c", tmp.name],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL
        )
        time.sleep(1.2)

        proxies = {
            "http": f"socks5h://127.0.0.1:{port}",
            "https": f"socks5h://127.0.0.1:{port}"
        }

        # Двойной тест для отсечения заглушек
        for test_url in TEST_URLS:
            resp = requests.get(test_url, proxies=proxies, timeout=TIMEOUT)
            if resp.status_code in (200, 204):
                return raw

        return None
    except Exception:
        return None
    finally:
        if proc:
            proc.terminate()
            try:
                proc.wait(timeout=2)
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
        f.write("# checked: REAL sing-box validation ✅\n\n")
        for i, raw in enumerate(nodes, 1):
            raw_clean = raw.split("#")[0]
            parsed = parse_config(raw_clean)
            host = parsed.get("host", "node") if parsed else "node"
            f.write(f"{raw_clean}#[{i:03d}] 🧊 YKTFLOW | {host}\n")
    print(f"💾 Сохранено: {path}")

# --- MAIN ---

def main():
    print("🧊 YKTFLOW Checker | Hardened sing-box validator")

    if not os.path.exists(INPUT_FILE):
        print(f"❌ Файл не найден: {INPUT_FILE}")
        return

    re_cfg = re.compile(r'(vless|vmess|trojan|ss)://[^\s]+')
    seen_unique = set()
    configs = []

    with open(INPUT_FILE, "r", encoding="utf-8") as f:
        for line in f:
            m = re_cfg.search(line.strip())
            if m:
                raw_url = m.group(0)
                parsed = parse_config(raw_url)
                if parsed:
                    # Уникальный ключ дедупликации без комментариев
                    key = f"{parsed['scheme']}://{parsed.get('uuid') or parsed.get('password')}@{parsed['host']}:{parsed['port']}"
                    if key not in seen_unique:
                        seen_unique.add(key)
                        configs.append(raw_url)

    print(f"📋 Найдено уникальных кандидатов: {len(configs)}")

    checked = 0
    found = 0
    stop_event = threading.Event()

    with ThreadPoolExecutor(max_workers=THREADS) as executor:
        futures = {executor.submit(check_config, raw): raw for raw in configs}

        for future in as_completed(futures):
            if stop_event.is_set():
                break

            checked += 1
            result = future.result()

            if result:
                found += 1
                with results_lock:
                    working
