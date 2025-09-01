from fastapi import FastAPI, Form, File, UploadFile
from app.models import Message
import requests
import base64
import socket
import os
import json
import threading
import hashlib
from typing import List, Optional,Union
from datetime import datetime
from uuid import uuid4

app = FastAPI()
connected_devices = []


RECEIVED_DIR = r"received_media"
MESSAGES_FILE = os.path.join(RECEIVED_DIR, "messages_history.json")
os.makedirs(RECEIVED_DIR, exist_ok=True)

# Use your exact structure
received_messages: List[dict] = []

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
            return {"success": True, "data": response.json()}
        except ValueError:
            return {
                "success": True,
                "status": "sent, but non-JSON response from Go",
                "raw_response": response.text
            }
    except Exception as e:
        return {"error": str(e)}

@app.post("/go_message")
async def go_message(
    sender: str = Form(...),
    receiver: str = Form(...),
    message_type: str = Form(...),      # "text" or mime like "image/png"
    message: Optional[str] = Form(""),
    files: Union[UploadFile, List[UploadFile], None] = File(None),  # ← accept one or many
):
    entry = {
        "timestamp": datetime.utcnow().isoformat() + "Z",
        "sender": sender,
        "receiver": receiver,
        "message_type": message_type,
        "txt_message": message or "",
        "payload": [],
    }

    # normalize to list
    file_list: List[UploadFile] = []
    if files is None:
        file_list = []
    elif isinstance(files, list):
        file_list = files
    else:
        file_list = [files]

    if file_list:
        for f in file_list:
            base = os.path.basename(f.filename) if f.filename else "file.bin"
            root, ext = os.path.splitext(base)
            safe_name = f"{root}_{uuid4().hex[:8]}{ext}"
            path = os.path.join(RECEIVED_DIR, safe_name)

            with open(path, "wb") as out:
                while True:
                    chunk = await f.read(1024 * 1024)
                    if not chunk:
                        break
                    out.write(chunk)

            entry["payload"].append({
                "name": safe_name,
                "original_name": base,
                "content_type": f.content_type or "",
                "size": os.path.getsize(path),
            })

    received_messages.append(entry)
    save_messages()
    return {"status": "ok", "files_saved": len(entry["payload"])}


@app.get("/receive")
def get_messages(receiver: Optional[str] = None):
    try:
        filtered_messages = received_messages
        if receiver:
            filtered_messages = [m for m in filtered_messages if m["receiver"] == receiver]
        filtered_messages = sorted(filtered_messages, key=lambda x: x["timestamp"], reverse=True)
        return filtered_messages
    except Exception as e:
        return {"error": str(e)}


def save_messages():
    """Save messages to JSON file"""
    try:
        with open(MESSAGES_FILE, "w", encoding="utf-8") as f:
            json.dump(received_messages, f, indent=2, default=str, ensure_ascii=False)
        print(f"[SAVE] Saved {len(received_messages)} messages to history")
    except Exception as e:
        print(f"[ERROR] Failed to save messages: {e}")