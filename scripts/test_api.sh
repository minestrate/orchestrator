#!/bin/bash

# Configuration
API_URL="http://localhost:8080"
JWT=$1

# Check for dependencies
if ! command -v jq &> /dev/null; then
    echo "Error: 'jq' is not installed. Please install it to run this script."
    exit 1
fi
ù
if [ -z "$JWT" ]; then
    echo "Usage: ./test_api.sh <JWT>"
    echo "Tip: Run the server first and copy the 'Dev JWT' from the output."
    exit 1
fi

set -e # Exit on error

echo "--- 1. List Servers (Initial) ---"
curl -s -H "Authorization: Bearer $JWT" "$API_URL/servers" | jq .

echo -e "\n--- 2. Create Server ---"
CREATE_RES=$(curl -s -X POST -H "Authorization: Bearer $JWT" \
    -H "Content-Type: application/json" \
    -d '{"game": "skywars", "players": 8}' \
    "$API_URL/servers")
echo "$CREATE_RES" | jq .

SERVER_ID=$(echo "$CREATE_RES" | jq -r .id)

if [ "$SERVER_ID" == "null" ] || [ -z "$SERVER_ID" ]; then
    echo "Error: Failed to create server."
    exit 1
fi

echo -e "\n--- 3. Get Server Status ($SERVER_ID) ---"
# Wait a brief moment for the FSM to transition if needed
sleep 1
curl -s -H "Authorization: Bearer $JWT" "$API_URL/servers/$SERVER_ID" | jq .

echo -e "\n--- 4. List Servers (After Creation) ---"
curl -s -H "Authorization: Bearer $JWT" "$API_URL/servers" | jq .

echo -e "\n--- 5. Delete Server ($SERVER_ID) ---"
STATUS_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE -H "Authorization: Bearer $JWT" "$API_URL/servers/$SERVER_ID")
echo "Delete Status Code: $STATUS_CODE"

echo -e "\n--- 6. Verify Deletion ---"
curl -s -H "Authorization: Bearer $JWT" "$API_URL/servers" | jq .

echo -e "\nDone."
