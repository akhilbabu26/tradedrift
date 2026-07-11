$outDir = "c:\Users\AKHIL BABU\OneDrive\Desktop\tradedrift\docs\07_Database\diagrams"
if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir }

function Create-SvgBase($width, $height) {
    return @"
<svg xmlns="http://www.w3.org/2000/svg" width="$width" height="$height" viewBox="0 0 $width $height">
  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 1.5 L 8 5 L 0 8.5 z" fill="#555555" />
    </marker>
    <marker id="one-to-many" viewBox="0 0 20 10" refX="18" refY="5" markerWidth="10" markerHeight="6" orient="auto-start-reverse">
      <!-- Crow's foot representation for ER diagram -->
      <path d="M 0 5 L 18 5 M 10 0 L 18 5 L 10 10" fill="none" stroke="#555555" stroke-width="1.5" />
      <circle cx="6" cy="5" r="2" fill="white" stroke="#555555" stroke-width="1.5" />
    </marker>
  </defs>
  <!-- Background -->
  <rect width="100%" height="100%" fill="#fafafa" />
"@
}

function Close-SvgBase {
    return "</svg>"
}

function Add-SvgTable($name, $columns, $x, $y, $width=240) {
    $headerHeight = 30
    $rowHeight = 24
    $totalHeight = $headerHeight + ($columns.Length * $rowHeight) + 10
    $escapedName = [System.Security.SecurityElement]::Escape($name)
    # Table border & background
    $xml = "  <!-- Table: $name -->`n"
    $xml += "  <rect x=`"$x`" y=`"$y`" width=`"$width`" height=`"$totalHeight`" rx=`"6`" ry=`"6`" fill=`"#ffffff`" stroke=`"#4e5d6c`" stroke-width=`"1.5`" />`n"
    
    # Header block
    $xml += "  <path d=`"M $x $($y + 6) A 6 6 0 0 1 $($x + 6) $y L $($x + $width - 6) $y A 6 6 0 0 1 $($x + $width) $($y + 6) L $($x + $width) $($y + $headerHeight) L $x $($y + $headerHeight) Z`" fill=`"#4e5d6c`" />`n"
    $xml += "  <text x=`"$($x + $width / 2)`" y=`"$($y + 20)`" font-family=`"Segoe UI, sans-serif`" font-size=`"13`" font-weight=`"bold`" text-anchor=`"middle`" fill=`"#ffffff`">$escapedName</text>`n"
    
    # Column records
    for ($i = 0; $i -lt $columns.Length; $i++) {
        $yOffset = $y + $headerHeight + ($i * $rowHeight) + 18
        $col = $columns[$i]
        $color = "#2c3e50"
        $weight = "normal"
        if ($col.Contains("[PK]")) { 
            $color = "#c0392b" 
            $weight = "bold"
        } elseif ($col.Contains("[FK]")) { 
            $color = "#2980b9" 
        }
        
        $escapedCol = [System.Security.SecurityElement]::Escape($col)
        $xml += "  <text x=`"$($x + 12)`" y=`"$yOffset`" font-family=`"Consolas, Monaco, monospace`" font-size=`"11`" font-weight=`"$weight`" fill=`"$color`">$escapedCol</text>`n"
    }
    return $xml
}

function Add-SvgNode($name, $x, $y, $width=160, $height=50, $fillColor="#dae8fc", $strokeColor="#6c8ebf") {
    $escapedName = [System.Security.SecurityElement]::Escape($name)
    $xml = "  <rect x=`"$x`" y=`"$y`" width=`"$width`" height=`"$height`" rx=`"6`" ry=`"6`" fill=`"$fillColor`" stroke=`"$strokeColor`" stroke-width=`"2`" />`n"
    $xml += "  <text x=`"$($x + $width / 2)`" y=`"$($y + $height / 2 + 5)`" font-family=`"Segoe UI, sans-serif`" font-size=`"12`" font-weight=`"bold`" text-anchor=`"middle`" fill=`"#2c3e50`">$escapedName</text>`n"
    return $xml
}

function Add-SvgCylinder($name, $x, $y, $width=120, $height=60, $fillColor="#ffe6cc", $strokeColor="#d79b00") {
    $escapedName = [System.Security.SecurityElement]::Escape($name)
    # Custom cylinder SVG path drawing
    $rx = $width / 2
    $ry = 10
    $xml = "  <!-- Cylinder: $name -->`n"
    # Base body
    $xml += "  <path d=`"M $x $($y + $ry) L $x $($y + $height - $ry) A $rx $ry 0 0 0 $($x + $width) $($y + $height - $ry) L $($x + $width) $($y + $ry) A $rx $ry 0 0 1 $x $($y + $ry) Z`" fill=`"$fillColor`" stroke=`"$strokeColor`" stroke-width=`"2`" />`n"
    # Top lid
    $xml += "  <ellipse cx=`"$($x + $rx)`" cy=`"$($y + $ry)`" rx=`"$rx`" ry=`"$ry`" fill=`"$fillColor`" stroke=`"$strokeColor`" stroke-width=`"2`" />`n"
    $xml += "  <text x=`"$($x + $width / 2)`" y=`"$($y + $height / 2 + 8)`" font-family=`"Segoe UI, sans-serif`" font-size=`"12`" font-weight=`"bold`" text-anchor=`"middle`" fill=`"#2c3e50`">$escapedName</text>`n"
    return $xml
}

function Add-SvgEdge($x1, $y1, $x2, $y2, $label="", $marker="arrow") {
    $xml = "  <line x1=`"$x1`" y1=`"$y1`" x2=`"$x2`" y2=`"$y2`" stroke=`"#555555`" stroke-width=`"1.5`" marker-end=`"url(#$marker)`" />`n"
    if ($label -ne "") {
        $midX = ($x1 + $x2) / 2
        $midY = (($y1 + $y2) / 2) - 6
        $xml += "  <text x=`"$midX`" y=`"$midY`" font-family=`"Segoe UI, sans-serif`" font-size=`"10`" font-weight=`"bold`" fill=`"#777777`" text-anchor=`"middle`">$label</text>`n"
    }
    return $xml
}

# 1. Database_Ownership.svg
$xml = Create-SvgBase 750 900
$services = @(
    ("Auth Service", 100, 50),
    ("Wallet Service", 100, 150),
    ("Order Service", 100, 250),
    ("Settlement Service", 100, 350),
    ("Portfolio Service", 100, 450),
    ("Trade Service", 100, 550),
    ("Notification Service", 100, 650),
    ("Market Service", 100, 750)
)
$databases = @(
    ("Auth DB", 500, 45),
    ("Wallet DB", 500, 145),
    ("Order DB", 500, 245),
    ("Settlement DB", 500, 345),
    ("Portfolio DB", 500, 445),
    ("Trade DB", 500, 545),
    ("Notification DB", 500, 645),
    ("Market DB", 500, 745)
)
foreach ($svc in $services) {
    $xml += Add-SvgNode $svc[0] $svc[1] $svc[2] 180 50 "#dae8fc" "#6c8ebf"
}
foreach ($db in $databases) {
    $xml += Add-SvgCylinder $db[0] $db[1] $db[2] 140 60 "#ffe6cc" "#d79b00"
}
for ($i = 0; $i -lt $services.Length; $i++) {
    $xml += Add-SvgEdge 280 ($services[$i][2] + 25) 490 ($databases[$i][2] + 30) "owns"
}
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Database_Ownership.svg" -Encoding utf8

# 2. Auth_ER.svg
$xml = Create-SvgBase 700 450
$users = @("+ id : UUID [PK]", "  email : VARCHAR(255)", "  username : VARCHAR(64)", "  password_hash : VARCHAR(255)", "  status : VARCHAR(20)", "  failed_login_attempts : INT", "  locked_until : TIMESTAMPTZ", "  created_at : TIMESTAMPTZ", "  updated_at : TIMESTAMPTZ")
$tokens = @("+ id : UUID [PK]", "- user_id : UUID [FK]", "  token_hash : VARCHAR(255)", "  status : VARCHAR(20)", "  expires_at : TIMESTAMPTZ", "  created_at : TIMESTAMPTZ")
$blacklist = @("+ jti : UUID [PK]", "  user_id : UUID", "  expires_at : TIMESTAMPTZ")
$xml += Add-SvgTable "users" $users 50 50 240
$xml += Add-SvgTable "refresh_tokens" $tokens 380 50 240
$xml += Add-SvgTable "blacklisted_tokens" $blacklist 380 270 240
# Connection users -> refresh_tokens
$xml += Add-SvgEdge 290 120 370 120 "" "one-to-many"
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Auth_ER.svg" -Encoding utf8

# 3. Wallet_ER.svg
$xml = Create-SvgBase 700 560
$assets = @("+ asset_code : VARCHAR(10) [PK]", "  asset_name : VARCHAR(50)", "  decimals : INT", "  is_enabled : BOOLEAN", "  seed_amount : DECIMAL(30,10)", "  display_order : INT")
$wallets = @("+ id : UUID [PK]", "  user_id : UUID", "- asset : VARCHAR(10) [FK]", "  available_balance : DECIMAL(30,10)", "  reserved_balance : DECIMAL(30,10)", "  is_frozen : BOOLEAN", "  total_balance : DECIMAL(30,10)")
$reservations = @("+ id : UUID [PK]", "  order_id : UUID", "  user_id : UUID", "  asset : VARCHAR(10)", "  reserved_amount : DECIMAL(30,10)", "  consumed_amount : DECIMAL(30,10)", "  status : VARCHAR(20)")
$transactions = @("+ id : UUID [PK]", "- wallet_id : UUID [FK]", "  reference_id : UUID", "  reference_type : VARCHAR(30)", "  transaction_type : VARCHAR(10)", "  asset : VARCHAR(10)", "  amount : DECIMAL(30,10)")
$xml += Add-SvgTable "supported_assets" $assets 50 50 240
$xml += Add-SvgTable "wallets" $wallets 380 50 240
$xml += Add-SvgTable "wallet_reservations" $reservations 50 280 240
$xml += Add-SvgTable "wallet_transactions" $transactions 380 280 240
# Connections: assets -> wallets, wallets -> transactions
$xml += Add-SvgEdge 290 100 370 100 "" "one-to-many"
$xml += Add-SvgEdge 500 240 500 270 "" "one-to-many"
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Wallet_ER.svg" -Encoding utf8

# 4. Order_ER.svg
$xml = Create-SvgBase 700 350
$orders = @("+ id : UUID [PK]", "  user_id : UUID", "  market_id : VARCHAR(20)", "  side : VARCHAR(10)", "  order_type : VARCHAR(10)", "  price : DECIMAL(30,10)", "  quantity : DECIMAL(30,10)", "  filled_quantity : DECIMAL(30,10)", "  remaining_quantity : DECIMAL(30,10)", "  status : VARCHAR(20)")
$outbox = @("+ id : UUID [PK]", "  aggregate_id : UUID", "  event_type : VARCHAR(50)", "  payload : JSONB", "  partition_key : VARCHAR(100)", "  status : VARCHAR(20)", "  failed_reason : TEXT", "  created_at : TIMESTAMPTZ")
$xml += Add-SvgTable "orders" $orders 50 30 240
$xml += Add-SvgTable "outbox" $outbox 380 30 240
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Order_ER.svg" -Encoding utf8

# 5. Settlement_ER.svg
$xml = Create-SvgBase 450 350
$settled = @("+ trade_id : UUID [PK]", "  buyer_id : UUID", "  seller_id : UUID", "  market_id : VARCHAR(20)", "  price : DECIMAL(30,10)", "  quantity : DECIMAL(30,10)", "  status : VARCHAR(20)", "  settled_at : TIMESTAMPTZ")
$xml += Add-SvgTable "settled_trades" $settled 100 50 240
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Settlement_ER.svg" -Encoding utf8

# 6. Portfolio_ER.svg
$xml = Create-SvgBase 700 300
$holdings = @("+ id : UUID [PK]", "  user_id : UUID", "  asset : VARCHAR(10)", "  total_quantity : DECIMAL(30,10)", "  average_entry_price : DECIMAL(30,10)")
$processed = @("+ trade_id : UUID [PK]", "  user_id : UUID", "  processed_at : TIMESTAMPTZ")
$xml += Add-SvgTable "holdings" $holdings 50 50 240
$xml += Add-SvgTable "processed_trades" $processed 380 50 240
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Portfolio_ER.svg" -Encoding utf8

# 7. Trade_ER.svg
$xml = Create-SvgBase 450 380
$trades = @("+ id : UUID [PK]", "  market_id : VARCHAR(20)", "  buyer_id : UUID", "  seller_id : UUID", "  buy_order_id : UUID", "  sell_order_id : UUID", "  price : DECIMAL(30,10)", "  quantity : DECIMAL(30,10)", "  executed_at : TIMESTAMPTZ")
$xml += Add-SvgTable "trades" $trades 100 50 240
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Trade_ER.svg" -Encoding utf8

# 8. Notification_ER.svg
$xml = Create-SvgBase 700 300
$notifications = @("+ id : UUID [PK]", "  user_id : UUID", "  title : VARCHAR(255)", "  message : TEXT", "  type : VARCHAR(30)", "  is_read : BOOLEAN", "  created_at : TIMESTAMPTZ")
$events = @("+ event_id : UUID [PK]", "  user_id : UUID", "  processed_at : TIMESTAMPTZ")
$xml += Add-SvgTable "notifications" $notifications 50 40 240
$xml += Add-SvgTable "processed_events" $events 380 40 240
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Notification_ER.svg" -Encoding utf8

# 9. Market_ER.svg
$xml = Create-SvgBase 700 320
$markets = @("+ id : VARCHAR(20) [PK]", "  base_asset : VARCHAR(10)", "  quote_asset : VARCHAR(10)", "  tick_size : DECIMAL(30,10)", "  lot_size : DECIMAL(30,10)", "  is_enabled : BOOLEAN")
$stats = @("+ market_id : VARCHAR(20) [PK][FK]", "+ window_date : DATE [PK]", "  open_price : DECIMAL(30,10)", "  high_price : DECIMAL(30,10)", "  low_price : DECIMAL(30,10)", "  close_price : DECIMAL(30,10)", "  volume : DECIMAL(30,10)")
$xml += Add-SvgTable "markets" $markets 50 40 240
$xml += Add-SvgTable "market_stats_daily" $stats 380 40 240
# Connection: markets -> stats
$xml += Add-SvgEdge 290 120 370 120 "" "one-to-many"
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Market_ER.svg" -Encoding utf8

# 10. Migration_Order.svg
$xml = Create-SvgBase 750 450
$steps = @(
    ("Extensions", 50, 50),
    ("Auth DB", 210, 50),
    ("Wallet DB", 370, 50),
    ("Market DB", 530, 50),
    ("Order DB", 50, 200),
    ("Settlement DB", 210, 200),
    ("Portfolio DB", 370, 200),
    ("Trade DB", 530, 200),
    ("Notification DB", 370, 320)
)
$nodes = @{}
foreach ($step in $steps) {
    $xml += Add-SvgNode $step[0] $step[1] $step[2] 120 50 "#e1d5e7" "#b85450"
}
# Connections
$xml += Add-SvgEdge 170 75 200 75
$xml += Add-SvgEdge 330 75 360 75
$xml += Add-SvgEdge 490 75 520 75
$xml += Add-SvgEdge 650 75 110 190 "Next"
$xml += Add-SvgEdge 170 225 200 225
$xml += Add-SvgEdge 330 225 360 225
$xml += Add-SvgEdge 490 225 520 225
$xml += Add-SvgEdge 590 250 430 315 "Settle Finish"
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Migration_Order.svg" -Encoding utf8

# 11. Transaction_Flow.svg
$xml = Create-SvgBase 950 450
$txNodes = @(
    ("Client: POST /orders", 30, 50),
    ("Order Service: Write Order + Outbox (Tx 1)", 30, 150),
    ("Outbox Publisher: Poll & Produce", 30, 250),
    ("Kafka Topic: orders.created", 30, 350),
    ("Matching Engine: Match & Produce", 330, 350),
    ("Kafka Topic: trades.executed", 330, 250),
    ("Settlement Svc: Consume & Call SettleTrade", 330, 150),
    ("Wallet Svc: Settle balance & Outbox (Tx 2)", 330, 50),
    ("Outbox Publisher: Poll & Produce Settled", 630, 50),
    ("Kafka Topic: user-trades.settled", 630, 150),
    ("Notification Svc: Write inbox & Push WS (Tx 3)", 630, 250),
    ("Client: WS Trade Fill Notification", 630, 350)
)
foreach ($node in $txNodes) {
    $fill = "#d5e8d4"
    $stroke = "#82b366"
    if ($node[0].Contains("Kafka")) { 
        $fill = "#f8cecc"
        $stroke = "#b85450"
    }
    $xml += Add-SvgNode $node[0] $node[1] $node[2] 260 50 $fill $stroke
}
for ($i = 0; $i -lt ($txNodes.Length - 1); $i++) {
    $xml += Add-SvgEdge ($txNodes[$i][1] + 130) ($txNodes[$i][2] + 50) ($txNodes[$i+1][1] + 130) ($txNodes[$i+1][2])
}
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Transaction_Flow.svg" -Encoding utf8

# 12. Query_Flow.svg
$xml = Create-SvgBase 750 520
$qNodes = @(
    ("Client Request", 100, 50),
    ("API Gateway", 100, 150),
    ("Wallet Service", 50, 280),
    ("Portfolio Service", 280, 280),
    ("Wallet DB: Read Balances", 50, 400),
    ("Portfolio DB: Read Holdings", 280, 400),
    ("Redis: Read Tickers", 510, 400)
)
foreach ($node in $qNodes) {
    $fill = "#fff2cc"
    $stroke = "#d6b656"
    if ($node[0].Contains("DB")) {
        $xml += Add-SvgCylinder $node[0] $node[1] $node[2] 160 60 "#ffe6cc" "#d79b00"
    } else {
        $xml += Add-SvgNode $node[0] $node[1] $node[2] 160 50 $fill $stroke
    }
}
$xml += Add-SvgEdge 180 100 180 145
$xml += Add-SvgEdge 150 200 130 275 "GET /balances"
$xml += Add-SvgEdge 210 200 330 275 "GET /portfolio/summary"
$xml += Add-SvgEdge 130 330 130 395
$xml += Add-SvgEdge 360 330 360 395
$xml += Add-SvgEdge 360 305 220 305 "gRPC: GetBalance"
$xml += Add-SvgEdge 440 305 590 395 ""
$xml += Close-SvgBase
$xml | Out-File -FilePath "$outDir\Query_Flow.svg" -Encoding utf8

Write-Host "All 12 SVG diagrams generated successfully!"
