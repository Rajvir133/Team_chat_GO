import base64
import json
import requests


# with open("v3.mp4", "rb") as f:
#     encoded = base64.b64encode(f.read()).decode()

with open("p1.jpg", "rb") as f:
    encoded = base64.b64encode(f.read()).decode()


# message = {
#     "sender": "192.168.29.207",
#     "receiver": "192.168.29.53",
#     "message_type": "video",
#     "message": "Sending an image via TCP/UDP setup 🚀",
#     "payload": [
#         {
#             "name": "v3.mp4",
#             "type": "video/mp4",
#             "data": encoded
#         }
#     ]
# }

message = {
    "sender": "192.168.29.207",
    "receiver": "192.168.29.207",
    "message_type": "image",
    "message": "Sending an image via TCP/UDP setup 🚀",
    "payload": [
        {
            "name": "p1.jpg",
            "type": "image/jpg",
            "data": encoded
        }
    ]
}


# message = {
#     "sender": "192.168.29.207",
#     "receiver": "192.168.29.207",
#     "message_type": "text",
#     "message": "Sending an image via TCP/UDP setup 🚀",
#     "payload": []
# }
# Step 3: Save the JSON to file
with open("message.json", "w") as f:
    json.dump(message, f, indent=4)

# Step 4: Read the JSON back (or just reuse `message`)
with open("message.json", "r") as f:
    payload = json.load(f)


print()
# Step 5: Send to FastAPI server
response = requests.post("http://localhost:5000/send", json=payload)

# Step 6: Print the response
try:
    print("✅ Response:", response.json())
except Exception:
    print("⚠️ Non-JSON response:", response.text)



