param (
    [Parameter(Mandatory=$true, HelpMessage="The Dev JWT token from the server output")]
    [string]$JWT,

    [Parameter(Mandatory=$false)]
    [string]$BaseUrl = "http://localhost:8080"
)

$ErrorActionPreference = "Stop"

# Helper for headers
$headers = @{
    "Authorization" = "Bearer $JWT"
}

Write-Host "--- 1. List Servers (Initial) ---" -ForegroundColor Cyan
Invoke-RestMethod -Uri "$BaseUrl/servers" -Headers $headers | Format-Table

Write-Host "`n--- 2. Create Server ---" -ForegroundColor Cyan
$createBody = @{
    game = "skywars"
    players = 8
} | ConvertTo-Json

$createRes = Invoke-RestMethod -Uri "$BaseUrl/servers" -Method Post -Headers $headers -ContentType "application/json" -Body $createBody
$createRes | ConvertTo-Json
$serverId = $createRes.id

if (-not $serverId) {
    Write-Error "Failed to create server."
    return
}

Write-Host "`n--- 3. Get Server Status ($serverId) ---" -ForegroundColor Cyan
Start-Sleep -Seconds 1
$status = Invoke-RestMethod -Uri "$BaseUrl/servers/$serverId" -Headers $headers
$status | ConvertTo-Json

Write-Host "`n--- 4. List Servers (After Creation) ---" -ForegroundColor Cyan
Invoke-RestMethod -Uri "$BaseUrl/servers" -Headers $headers | Format-Table

Write-Host "`n--- 5. Delete Server ($serverId) ---" -ForegroundColor Cyan
$deleteRes = Invoke-WebRequest -Uri "$BaseUrl/servers/$serverId" -Method Delete -Headers $headers -UseBasicParsing
Write-Host "Delete Status Code: $($deleteRes.StatusCode)"

Write-Host "`n--- 6. Verify Deletion ---" -ForegroundColor Cyan
Invoke-RestMethod -Uri "$BaseUrl/servers" -Headers $headers | Format-Table

Write-Host "`nDone." -ForegroundColor Green
