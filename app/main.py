from fastapi import FastAPI, Query,jsonify
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
            return {"success":True,"data":response}
        except ValueError:
            return {
                "status": "sent, but non-JSON response from Go",
                "raw_response": response.text
            }
    except Exception as e:
        return {"error": str(e)}


@app.post("/go_message")
async def receive_message(msg: Message):
    print(f"[RECEIVED] {msg.message_type} from {msg.sender} → {msg.receiver}")

    entry = {
        "sender": msg.sender,
        "receiver": msg.receiver,
        "message_type": msg.message_type,
        "txt_message": msg.message,
        "timestamp": datetime.utcnow().isoformat(),
        "payload": []
    }

    try:
        if msg.message_type == "text":
            print(f"[TEXT] Pure text message received")

            if msg.payload:
                entry["payload"] = []
            
        # Handle file messages (images, videos, documents)  
        elif msg.message_type.startswith(("image/", "video/")):
            print(f"[FILE] Processing file message")
            if msg.payload:
                for f in msg.payload:
                    try:
                        # Decode base64 data
                        raw = base64.b64decode(f.data) if f.data else b""
                        file_path = os.path.join(RECEIVED_DIR, f.name)
                        
                        with open(file_path, "wb") as fh:
                            fh.write(raw)

                        entry["payload"].append({
                            "name": f.name,
                            "type": f.type,
                            "size": len(raw),
                        })                        
                        print(f"[✓] Saved {f.name} ({len(raw)} bytes)")
                        
                    except Exception as e:
                        print(f"[!] Failed to save {f.name}: {e}")
                        entry["payload"].append({
                            "name": f.name if hasattr(f, 'name') else 'unknown',
                            "type": f.type if hasattr(f, 'type') else 'unknown', 
                            "size": 0,
                            "error": str(e)
                        })
        
        # Handle unknown message types gracefully
        else:
            print(f"[WARN] Unknown message type: {msg.message_type}")
            entry["payload"] = []

    except Exception as e:
        print(f"[ERROR] Failed to process message: {e}")
        entry["error"] = str(e)

    # Always save the message
    received_messages.append(entry)
    save_messages()
    
    return {
        "status": "received",
        "message_type": msg.message_type,
        "payload_count": len(entry.get("payload", []))
    }


@app.get("/receive/{sender_ip}")
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


def save_messages():
    """Save messages to JSON file"""
    try:
        with open(MESSAGES_FILE, "w", encoding="utf-8") as f:
            json.dump(received_messages, f, indent=2, default=str, ensure_ascii=False)
        print(f"[SAVE] Saved {len(received_messages)} messages to history")
    except Exception as e:
        print(f"[ERROR] Failed to save messages: {e}")