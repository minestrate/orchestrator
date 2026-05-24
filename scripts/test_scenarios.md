# API Test Scenarios

These commands can be run from PowerShell to verify the API functionality.

## 0. Setup: Run the Server
In a separate terminal:
```powershell
go run .\cmd\minestrate\main.go --config .\config\config.example.yaml
```
Copy the **Dev JWT** from the output.

## 1. Set Token Variable
```powershell
$JWT = "PASTE_YOUR_TOKEN_HERE"
$BASE_URL = "http://localhost:8080"
```

## 2. List Servers
```powershell
Invoke-RestMethod -Uri "$BASE_URL/servers" -Headers @{Authorization="Bearer $JWT"}
```

## 3. Create a Server
```powershell
$createRes = Invoke-RestMethod -Uri "$BASE_URL/servers" -Method Post -Headers @{Authorization="Bearer $JWT"} -ContentType "application/json" -Body '{"game": "skywars", "players": 8}'
$createRes | ConvertTo-Json
$SERVER_ID = $createRes.id
```

## 4. Get Server Details
```powershell
Invoke-RestMethod -Uri "$BASE_URL/servers/$SERVER_ID" -Headers @{Authorization="Bearer $JWT"} | ConvertTo-Json
```

## 5. Delete Server
```powershell
Invoke-WebRequest -Uri "$BASE_URL/servers/$SERVER_ID" -Method Delete -Headers @{Authorization="Bearer $JWT"}
```

## 6. Unauthorized Request (No Token)
```powershell
Invoke-RestMethod -Uri "$BASE_URL/servers"
# Should return 401 Unauthorized
```
