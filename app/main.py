from fastapi import FastAPI, Form, File, UploadFile
import requests
import os
import json
from typing import List, Optional,Union, Tuple
from datetime import datetime
from uuid import uuid4

app = FastAPI()
connected_devices = []


RECEIVED_DIR = r"received_media"
MESSAGES_FILE = os.path.join(RECEIVED_DIR, "messages_history.json")
os.makedirs(RECEIVED_DIR, exist_ok=True)

# Use your exact structure
received_messages: List[dict] = []






# 🔍 Scan local IP range for devices with TCP port 9200 open
@app.get("/scan")
def scan_devices():
    try:
        response = requests.get("http://localhost:8080/scan")
        return response.json()
    except Exception as e:
        return {"error": str(e)}








# 📤 Send message to a known device (proxy to Go server)
@app.post("/send")
def send_message(
    sender: str = Form(...),
    receiver: str = Form(...),
    message_type: str = Form(...),
    message: Optional[str] = Form(""),
    files: Union[UploadFile, List[UploadFile], None] = File(None),
):
    try:
        msg, blobs = create_message(sender, receiver, message_type, message, files)
        
        
        if not blobs:
            files_param = [("files", ("", b"", "application/octet-stream"))]
            msg["message_type"] = "text"
        else:
            files_param = [("files", (name, content, ctype)) for (name, content, ctype) in blobs]
        
        resp = requests.post("http://localhost:8080/send", data=msg, files=files_param)
        try:
            return resp.json()
        except ValueError:
            return {"status": resp.status_code, "body": resp.text}
    except Exception as e:
        return {"error": str(e)}





@app.post("/go_message")
def go_message(
    sender: str = Form(...),
    receiver: str = Form(...),
    message_type: str = Form(...),
    message: Optional[str] = Form(""),
    files: Union[UploadFile, List[UploadFile], None] = File(None),
):
    try:
        msg, blobs = create_message(sender, receiver, message_type, message, files)
        os.makedirs(RECEIVED_DIR, exist_ok=True)
        payload = []
        for name, content, ctype in blobs:
            base = os.path.basename(name)
            path = os.path.join(RECEIVED_DIR, base)
            with open(path, "wb") as out:
                out.write(content)
            payload.append({
                "name": base,
                "content_type": ctype,
                "size": len(content),
            })

        entry = {
            "timestamp": datetime.utcnow().isoformat() + "Z",
            **msg,
            "payload": payload,
        }
        received_messages.append(entry)
        save_messages()
        return {"status": "ok", "files_saved": len(payload)}
    except Exception as e:
        return {"error": str(e)}







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
        
           
def create_message(
    sender: str,
    receiver: str,
    message_type: str,
    message: Optional[str],
    files: Union[UploadFile, List[UploadFile], None],
) -> Tuple[dict, List[Tuple[str, bytes, str]]]:
    if not sender or not receiver or not message_type:
        raise ValueError("sender, receiver, and message_type are required")

    msg = {
        "sender": sender,
        "receiver": receiver,
        "message_type": message_type,
        "message": message or "",
    }

    # normalize files to a list
    if files is None:
        file_list: List[UploadFile] = []
    elif isinstance(files, list):
        file_list = files
    else:
        file_list = [files]

    blobs: List[Tuple[str, bytes, str]] = []
    for f in file_list:
        if f is None:
            continue
        content = f.file.read()
        filename = f.filename or "file.bin"
        ctype = f.content_type or "application/octet-stream"
        blobs.append((filename, content, ctype))

    return msg, blobs