from fastapi import FastAPI
from app.models import Message
import requests
import socket
import os
import json
import threading

app = FastAPI()
connected_devices = []
history_messages: list[Message] = []

RECEIVED_DIR = r"E:\go\message_in_go\received_media"
os.makedirs(RECEIVED_DIR, exist_ok=True)

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
        # Convert payload to dict for processing
        payload_dict = msg.dict()
        
        # If it's a media message, ensure data is in list format for JSON serialization
        if msg.message_type in ("image", "video") and isinstance(msg.payload, list):
            for file_payload in payload_dict["payload"]:
                if "data" in file_payload:
                    if isinstance(file_payload["data"], bytes):
                        # Convert bytes to list for JSON serialization
                        file_payload["data"] = list(file_payload["data"])
                    elif isinstance(file_payload["data"], str):
                        # Convert string to list of bytes
                        file_payload["data"] = list(file_payload["data"].encode('utf-8'))
                    elif not isinstance(file_payload["data"], list):
                        # Convert other types to list
                        file_payload["data"] = list(bytes(file_payload["data"]))
        
        print(f"[DEBUG] Sending payload to Go server: {type(payload_dict['payload'][0]['data'])}")
        response = requests.post("http://localhost:8080/send", json=payload_dict)
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
    print(f"[RECEIVED] {msg.message_type.upper()} message from {msg.sender} to {msg.receiver}")

    # Save the full message JSON
    json_path = os.path.join(RECEIVED_DIR, "msg.json")
    with open(json_path, "w") as f:
        json.dump(msg.dict(), f, indent=4)

    # Print and save the message field if present
    if msg.message:
        print(f"[MESSAGE] {msg.message}")
        # Optionally, save the message to a text file
        with open(os.path.join(RECEIVED_DIR, "last_message.txt"), "w") as f:
            f.write(msg.message)

    # Save media files if image or video
    if msg.message_type in ("image", "video"):
        for file in msg.payload:
            try:
                # Handle raw bytes directly
                if isinstance(file["data"], bytes):
                    file_bytes = file["data"]
                elif isinstance(file["data"], list):
                    # Convert list to bytes (from JSON)
                    file_bytes = bytes(file["data"])
                elif isinstance(file["data"], str):
                    # Convert string to bytes (for text data)
                    file_bytes = file["data"].encode('utf-8')
                else:
                    # Convert other types to bytes
                    file_bytes = bytes(file["data"])
                
                file_path = os.path.join(RECEIVED_DIR, file["name"])
                with open(file_path, "wb") as out_file:
                    out_file.write(file_bytes)
                print(f"[✓] Saved {file['name']} to received_media/")
            except Exception as e:
                print(f"[!] Failed to save {file.get('name', 'unknown')}: {e}")

    # Append to in-memory history
    history_messages.append(msg)
    return {"status": "received"}

@app.get("/history")
def history():
    try:
        return [m.dict() for m in history_messages]
    except Exception as e:
        return {"error": str(e)}
