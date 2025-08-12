python -m venv venv 

venv\scripts\activate

pip install -r requirements.txt

# Terminal 1
uvicorn app.main:app --reload --port 5000

#terminal 2
go run go_c/main.go

#if want to store detail logs 
go run go_c/main.go > go_c/app.log 2>&1