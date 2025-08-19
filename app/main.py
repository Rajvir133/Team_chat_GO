from fastapi import FastAPI, Query
from app.models import Message
import requests
import base64
import socket
import os
import json
import threading
import hashlib
from typing import List, Optional
from datetime import datetime

app = FastAPI()
connected_devices = []




RECEIVED_DIR = r"E:\go\message_in_go\received_media"
MESSAGES_FILE = os.path.join(RECEIVED_DIR, "messages_history.json")
os.makedirs(RECEIVED_DIR, exist_ok=True)

# Use your exact structure
received_messages: List[dict] = []






def load_messages():
    """Load messages from JSON file on startup"""
    global received_messages
    try:
        if os.path.exists(MESSAGES_FILE):
            with open(MESSAGES_FILE, "r", encoding="utf-8") as f:
                received_messages = json.load(f)
                print(f"[logs] Loaded {len(received_messages)} messages from history")
        else:
            print("[logs] No message history found, starting fresh")
    except Exception as e:
        print(f"[ERROR] Failed to load message history: {e}")
        received_messages = []
def save_messages():
    """Save messages to JSON file"""
    try:
        with open(MESSAGES_FILE, "w", encoding="utf-8") as f:
            json.dump(received_messages, f, indent=2, default=str, ensure_ascii=False)
        print(f"[SAVE] Saved {len(received_messages)} messages to history")
    except Exception as e:
        print(f"[ERROR] Failed to save messages: {e}")

# Load messages on startup
load_messages()









# 🔍 Scan local IP range for devices with TCP port 9000 open
@app.get("/scan")
def scan_devices():
    try:
        response = requests.get("http://localhost:8080/scan")
        return response.json()
    except Exception as e:
        return {"error": str(e)}










# 📤 Send message to a known device (proxy to Go server)
@app.post("/send")
def send_message(msg: Message):
    try:
        response = requests.post("http://localhost:8080/send", json=msg.dict())
        try:
            return response.json()
        except ValueError:
            return {
                "status": "sent, but non-JSON response from Go",
                "raw_response": response.text
            }
    except Exception as e:
        return {"error": str(e)}











@app.post("/receive")
async def receive_message(msg: Message):
    print(f"[RECEIVED] {msg.message_type} from {msg.sender} → {msg.receiver}\n")

    entry = {
        "sender": msg.sender,
        "receiver": msg.receiver,
        "message_type": msg.message_type,
        "txt_message": msg.message,
        "timestamp": datetime.utcnow().isoformat(),
        "payload": [] 
    }

    # save each file in payload (if any)
    if msg.message_type.startswith(("image/", "video/")):
        for f in msg.payload:
            try:
                # decode base-64
                raw = base64.b64decode(f.data) if f.data else b""
                file_path = os.path.join(RECEIVED_DIR, f.name)
                with open(file_path, "wb") as fh:
                    fh.write(raw)

                # record metadata
                entry["payload"].append({
                    "name":  f.name,
                    "type":  f.type,
                    "size":  len(raw),
                    "hash":  f.hash or "",
                    "path":  file_path
                })
                print(f"[✓] saved {f.name} ({len(raw)} bytes)")
            except Exception as e:
                print(f"[!] failed to save {f.name}: {e}")
    else:
        # non-file message (e.g. plain text)
        entry["payload"] = [p.dict() for p in msg.payload]

    received_messages.append(entry)
    save_messages()
    return {"status": "received"}



@app.get("/messages")
def get_messages(
    receiver: Optional[str] = None
):
    try:
        filtered_messages = received_messages
        
        if receiver:
            filtered_messages = [m for m in filtered_messages if m["receiver"] == receiver]
        filtered_messages = sorted(filtered_messages, key=lambda x: x["timestamp"], reverse=True)
        
        return filtered_messages
        
    except Exception as e:
        return {"error": str(e)}
