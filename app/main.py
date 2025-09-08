from fastapi import FastAPI, Form, File, UploadFile , Response, status
import requests
import os
import json
from typing import List, Optional,Union, Tuple
from datetime import datetime
import time, uuid
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
    rid = str(uuid.uuid4())
    try:
        resp = requests.get("http://localhost:8080/scan")
        if not resp.ok:
            return err(resp.status_code, "UPSTREAM_ERROR", f"go /scan returned {resp.status_code}", {"body": resp.text}, rid)
        return ok(resp.json(), rid=rid)
    except Exception as e:
        return err(status.HTTP_502_BAD_GATEWAY, "UPSTREAM_ERROR", str(e), rid=rid)








# 📤 Send message to a known device (proxy to Go server)
@app.post("/send")
def send_message(
    sender: str = Form(...),
    receiver: str = Form(...),
    message_type: str = Form(...),
    message: Optional[str] = Form(""),
    files: Union[UploadFile, List[UploadFile], None] = File(None),
):
    rid = str(uuid.uuid4())
    
    try:
        msg, blobs = create_message(sender, receiver, message_type, message, files)
        files_param = [("files", (n, b, c)) for (n, b, c) in blobs] or [("files", ("", b"", "application/octet-stream"))]
        
        resp = requests.post("http://localhost:8080/send", data=msg, files=files_param, timeout=60)
        
        body = resp.json() if resp.headers.get("content-type","").startswith("application/json") else {"raw": resp.text}
        
        if not resp.ok:
            return err(resp.status_code, "UPSTREAM_ERROR", "go /send failed", body, rid)
        return ok({"go": body}, rid=rid)
    
    except Exception as e:
        return err(status.HTTP_502_BAD_GATEWAY, "UPSTREAM_ERROR", str(e), rid=rid)





@app.post("/go_message")
def go_message(
    sender: str = Form(...),
    receiver: str = Form(...),
    message_type: str = Form(...),
    message: Optional[str] = Form(""),
    files: Union[UploadFile, List[UploadFile], None] = File(None),
):
    rid = str(uuid.uuid4())
    try:
        
        msg, blobs = create_message(sender, receiver, message_type, message, files)
        os.makedirs(RECEIVED_DIR, exist_ok=True)
        payload_meta = []
        
        for name, content, ctype in blobs:
            base = os.path.basename(name); root, ext = os.path.splitext(base)
            safe = base
            with open(os.path.join(RECEIVED_DIR, safe), "wb") as out: out.write(content)
            payload_meta.append({
                "original_name": base,
                "content_type": ctype,
                "size": len(content),
                })

        entry = {
            "timestamp": datetime.utcnow().isoformat()+"Z", 
            **msg,  
            "payload": payload_meta
            }
        
        received_messages.append(entry); save_messages()
        
        return ok({"files_saved": len(payload_meta), "message": entry}, rid=rid)
    except ValueError as ve:
        return err(status.HTTP_400_BAD_REQUEST, "BAD_REQUEST", str(ve), rid=rid)
    except Exception as e:
        return err(status.HTTP_500_INTERNAL_SERVER_ERROR, "INTERNAL_ERROR", str(e), rid=rid)







@app.get("/receive")
def get_messages(receiver: Optional[str] = None):
    t0 = time.perf_counter(); rid = str(uuid.uuid4())
    
    try:
        msgs = received_messages
        if receiver:
            msgs = [m for m in msgs if m["receiver"] == receiver]
        msgs = sorted(msgs, key=lambda x: x["timestamp"], reverse=True)
        return ok({"messages": msgs}, rid=rid)
    
    except Exception as e:
        return err(status.HTTP_500_INTERNAL_SERVER_ERROR, "INTERNAL_ERROR", str(e), rid=rid)












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


def ok(data, rid=None):
    return {
        "success": True,
        "data": data,
        "meta": {
            "request_id": rid or str(uuid.uuid4()),
        }
    }

def err(code: int, error_code: str, message: str, details=None, rid=None):
    return Response(
        content=json.dumps({
            "success": False,
            "error": {"code": error_code, "message": message, "details": details or {}},
            "meta": {"request_id": rid or str(uuid.uuid4())}
        }),
        media_type="application/json",
        status_code=code
    )