# Examples

This directory contains examples and usage demonstrations for Patris Export.

- [WebSocat terminal inspection](websocat.md)
- [Transform, raw, export, and send update examples](export-transform-send.md)

## Basic Usage Examples

### 1. Converting a Database to JSON

```bash
# Simple conversion
patris-export convert database.db -f json

# With a custom/debug character map override
patris-export convert database.db -c custom-farsi-chars.txt -f json

# Specify output directory
patris-export convert database.db -f json -o output/
```

### 2. Converting to CSV

```bash
# Convert to CSV format
patris-export convert database.db -f csv

# Convert to CSV with embedded Patris81 mapping
patris-export convert database.db -f csv -o output/
```

### 3. File Watching

Watch a database file and automatically convert it when it changes:

```bash
# Watch and auto-convert to JSON
patris-export convert database.db -f json -w

# Watch and auto-convert to CSV
patris-export convert database.db -f csv -w
```

### 4. Database Information

View schema and metadata about a database:

```bash
patris-export info database.db
```

Output:
```
📋 Database Information
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📁 File: database.db
📊 Records: 354
📝 Fields: 28

🗂️  Field Definitions
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 1. Code                 long         (size: 4)
 2. Name                 alpha        (size: 55)
 ...
```

### 5. Company Information

Parse and display company.inf file:

```bash
patris-export company company.inf
```

Output:
```
🏢 Company Information
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📛 Name:       شرکت نمونه
📅 Start Date: 99.99.99
📅 End Date:   00.00.00
```

## REST API Server Examples

### Starting the Server

```bash
# Start on default port (8080)
patris-export serve database.db

# Start on custom port
patris-export serve database.db -a :3000

# Disable file watching
patris-export serve database.db -w=false
```

### Using the REST API

#### Get All Records

```bash
curl http://localhost:8080/api/records
```

Response:
```json
{
  "101": {
    "Name": "آی سی",
    "Serial": "101"
  }
}
```

#### Export Records as CSV

```bash
curl http://localhost:8080/api/records.csv -o records.csv
curl 'http://localhost:8080/api/records?format=csv&download=1' -o records.csv
curl -H 'Accept: text/csv' http://localhost:8080/api/records -o records.csv
```

CSV responses include `Code` as the first column, followed by the detected
record fields.

#### Download Records as Excel

```bash
curl 'http://localhost:8080/api/records.xlsx?download=1' -o records.xlsx
curl -H 'Accept: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' \
  http://localhost:8080/api/records -o records.xlsx
```

Use `rtl=1` for a right-to-left workbook view. The workbook uses the same
transformed records as the other sinks and includes a non-secret Metadata
sheet; no workbook transformation runs in the browser.

#### Get Database Info

```bash
curl http://localhost:8080/api/info
```

Response:
```json
{
  "success": true,
  "file": "database.db",
  "num_records": 354,
  "num_fields": 28,
  "fields": [...]
}
```

#### Web Interface

Open in browser:
```
http://localhost:8080
```

## WebSocket Examples

### JavaScript/Browser Example

```html
<!DOCTYPE html>
<html>
<head>
    <title>Patris Export WebSocket Demo</title>
</head>
<body>
    <h1>Live Database Updates</h1>
    <div id="status">Connecting...</div>
    <div id="data"></div>

    <script>
        const ws = new WebSocket('ws://localhost:8080/ws');
        
        ws.onopen = () => {
            document.getElementById('status').textContent = 'Connected ✅';
        };
        
        ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            console.log('Update received:', data);
            
            document.getElementById('data').innerHTML = `
                <p>Type: ${data.type}</p>
                <p>Time: ${data.timestamp}</p>
                <p>Records: ${data.count}</p>
                <pre>${JSON.stringify(data.records.slice(0, 5), null, 2)}</pre>
            `;
        };
        
        ws.onerror = (error) => {
            console.error('WebSocket error:', error);
            document.getElementById('status').textContent = 'Error ❌';
        };
        
        ws.onclose = () => {
            document.getElementById('status').textContent = 'Disconnected ⚠️';
        };
    </script>
</body>
</html>
```

### Node.js Example

```javascript
const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws');

ws.on('open', () => {
    console.log('✅ Connected to Patris Export WebSocket');
});

ws.on('message', (data) => {
    const update = JSON.parse(data);
    console.log(`📊 Received ${update.count} records at ${update.timestamp}`);
    
    // Process the records
    update.records.forEach((record, index) => {
        console.log(`Record ${index + 1}:`, record.Name);
    });
});

ws.on('error', (error) => {
    console.error('❌ WebSocket error:', error);
});

ws.on('close', () => {
    console.log('⚠️  Connection closed');
});
```

### Python Example

```python
import asyncio
import websockets
import json

async def watch_database():
    uri = "ws://localhost:8080/ws"
    
    async with websockets.connect(uri) as websocket:
        print("✅ Connected to Patris Export WebSocket")
        
        async for message in websocket:
            data = json.loads(message)
            print(f"📊 Received {data['count']} records at {data['timestamp']}")
            
            # Process first 5 records
            for i, record in enumerate(data['records'][:5]):
                print(f"  Record {i+1}: {record.get('Name', 'N/A')}")

asyncio.get_event_loop().run_until_complete(watch_database())
```

## Batch Processing Example

Process multiple database files:

```bash
#!/bin/bash

# Convert all .db files in a directory
for db_file in /path/to/databases/*.db; do
    echo "Converting $db_file..."
    patris-export convert "$db_file" \
        -f json \
        -o output/
done

echo "✅ All files converted!"
```

## Docker Example (Future)

```bash
# Build Docker image
docker build -t patris-export .

# Run conversion
docker run --rm \
    -v $(pwd)/data:/data \
    patris-export convert /data/database.db -f json -o /data/output

# Run server
docker run --rm -p 8080:8080 \
    -v $(pwd)/data:/data \
    patris-export serve /data/database.db
```

## Integration Examples

### Shell Script Integration

```bash
#!/bin/bash

# Convert database and upload to S3
patris-export convert database.db -f json -o /tmp
aws s3 cp /tmp/database.json s3://my-bucket/data/

# Convert and send to API
patris-export convert database.db -f json -o /tmp
curl -X POST https://api.example.com/data \
    -H "Content-Type: application/json" \
    -d @/tmp/database.json
```

### Cron Job Example

```cron
# Run conversion every hour
0 * * * * /usr/local/bin/patris-export convert /data/database.db -f json -o /var/www/data/

# Run at 2 AM daily
0 2 * * * /usr/local/bin/patris-export convert /data/database.db -f csv -o /backups/
```

## Advanced Usage

### Custom Processing Pipeline

```bash
# Convert to JSON and pipe through jq for filtering
patris-export convert database.db -f json -o - | \
    jq '[.[] | select(.Code > 100)]' > filtered.json

# Convert and gzip
patris-export convert database.db -f json -o /tmp/
gzip /tmp/database.json
```

### Multiple Format Export

```bash
#!/bin/bash

DB_FILE="database.db"
OUTPUT_DIR="export_$(date +%Y%m%d)"

mkdir -p "$OUTPUT_DIR"

# Export to multiple formats
patris-export convert "$DB_FILE" -f json -o "$OUTPUT_DIR"
patris-export convert "$DB_FILE" -f csv -o "$OUTPUT_DIR"

# Create archive
tar czf "$OUTPUT_DIR.tar.gz" "$OUTPUT_DIR"
echo "✅ Export complete: $OUTPUT_DIR.tar.gz"
```

## Troubleshooting Examples

### Verbose Output

```bash
# Enable verbose logging
patris-export -v convert database.db -f json
```

### Check Version

```bash
patris-export --version
```

### Test Database Connectivity

```bash
# Try to read database info first
patris-export info database.db

# If successful, proceed with conversion
patris-export convert database.db -f json
```

## Performance Tips

1. **Use JSON for Large Datasets**: JSON is generally faster for large datasets
2. **Disable Watching for One-Time Conversions**: Skip the `-w` flag for better performance
3. **Use Appropriate Output Directory**: Write to fast storage (SSD) when possible
4. **Batch Processing**: Process multiple files sequentially rather than in parallel

## Next Steps

- See [README.md](../README.md) for installation and setup
- See [TODO.md](../TODO.md) for planned features
- Visit the [GitHub repository](https://github.com/atomicdeploy/patris-export) for updates
