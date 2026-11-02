# TradeDrift — AWS EC2 (`ap-south-1`) Deployment Guide

This guide provides comprehensive, production-ready instructions for deploying the TradeDrift backend to an Amazon EC2 instance in the **Mumbai (`ap-south-1`)** region with HTTPS and WSS (secure WebSockets) connected to the Vercel frontend.

---

## 1. Capacity & Instance Sizing

### Memory & CPU Budget Breakdown
TradeDrift runs 17 Docker containers concurrently:

| Component | Minimum RAM | Recommended RAM | Notes |
| :--- | :---: | :---: | :--- |
| **Apache Kafka 3.7.0** (KRaft mode) | 1.5 GB | 2.0 GB | JVM heap + OS page cache for partition logs |
| **PostgreSQL 16** (11 databases) | 350 MB | 800 MB | Buffer pools, active connections across 11 DBs |
| **Redis 7** (In-memory depth & cache) | 100 MB | 250 MB | Low memory footprint |
| **13 Go Microservices** | 1.2 GB | 2.0 GB | ~80–150 MB per service |
| **Prometheus & Grafana** | 400 MB | 600 MB | Scrapes metrics every 15s |
| **OS & Buffer Overhead** | 500 MB | 1.0 GB | Ubuntu 24.04 kernel & Docker runtime |
| **Total Baseline** | **~4.05 GB** | **~6.65 GB** | **Target: 8 GB RAM** |

### Instance Recommendation
* **Recommended:** **`t3.large`** (2 vCPUs, **8 GB RAM**) in `ap-south-1` (~$60.74/month, or ~$0.0832/hour).
* **Storage:** 40 GB gp3 SSD (3000 IOPS, 125 MB/s baseline included at no additional cost).
* **Cost Comparison vs Managed Services:**
  * Single EC2 `t3.large` (All-in-one Compose): **~$67–$72/mo** (including EIP and storage).
  * AWS RDS PostgreSQL (db.t4g.small) + MSK (Kafka) + ElastiCache + ECS Fargate: **~$250–$400/mo**.
  * *Verdict:* For staging, demo, and portfolio evaluation, single EC2 deployment saves over 75% in monthly AWS infrastructure costs.

---

## 2. AWS Security Group Configuration

In the AWS EC2 Management Console, create a Security Group for the instance with the following inbound rules:

| Type | Port Range | Protocol | Source | Description |
| :--- | :---: | :---: | :--- | :--- |
| **SSH** | `22` | TCP | `Your_Public_IP/32` | Restrict SSH access strictly to your current admin IP |
| **HTTP** | `80` | TCP | `0.0.0.0/0` | Let's Encrypt certificate challenge & HTTP redirect |
| **HTTPS** | `443` | TCP | `0.0.0.0/0` | Public REST APIs and Secure WebSockets (`wss://`) |

> [!CAUTION]
> **Do NOT expose internal ports:** Keep ports `5432` (Postgres), `6379` (Redis), `29092` (Kafka), `50051–50059` (gRPC), `8080` (Gateway), `8082` (Matching), `8085` (Admin), and `9090` (Prometheus) closed to the internet. They operate exclusively inside Docker's internal private network (`tradedrift-prod-net`).

---

## 3. Provisioning the Linux EC2 Host

### Step 3.1: Connect to your EC2 Instance
```bash
ssh -i /path/to/your-key.pem ubuntu@<EC2_PUBLIC_IP>
```

### Step 3.2: System Update & Docker Installation
Run these commands on the Ubuntu EC2 instance:
```bash
# 1. Update OS packages
sudo apt-get update && sudo apt-get upgrade -y

# 2. Install prerequisites
sudo apt-get install -y ca-certificates curl gnupg lsb-release nginx certbot python3-certbot-nginx git

# 3. Install Docker Engine and Docker Compose Plugin
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# 4. Enable non-root docker management
sudo usermod -aG docker ubuntu

# 5. Configure Docker log rotation (critical for 40GB disk protection)
sudo tee /etc/docker/daemon.json << 'EOF'
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "50m",
    "max-file": "3"
  }
}
EOF
sudo systemctl restart docker

# 6. Configure 4GB Swap Space (safety net against burst OOM kills)
if [ ! -f /swapfile ]; then
    sudo fallocate -l 4G /swapfile
    sudo chmod 600 /swapfile
    sudo mkswap /swapfile
    sudo swapon /swapfile
    echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
fi

# Apply docker group changes to current subshell
newgrp docker
```

---

## 4. Deploying TradeDrift Application

### Step 4.1: Clone the Repository
> [!IMPORTANT]
> Ensure all local changes, scripts, and Docker Compose configurations on `deployment/production-readiness` have been committed and pushed to GitHub prior to cloning on EC2.

```bash
git clone https://github.com/akhilbabu26/tradedrift.git
cd tradedrift
git checkout deployment/production-readiness
```

### Step 4.2: Configure Production Environment Variables
TradeDrift standardizes on `.env.production` for all production Compose and migration commands:

```bash
# Copy template to .env.production (never overwrite existing production config)
cp -n .env.production.template .env.production

# Generate cryptographically strong random secrets:
JWT_KEY=$(openssl rand -hex 32)
RECON_KEY=$(openssl rand -hex 32)
TOPUP_KEY=$(openssl rand -hex 32)
PG_PASS=$(openssl rand -hex 24)
GRAFANA_PASS=$(openssl rand -hex 20)

# Replace placeholders in .env.production:
sed -i "s/REPLACE_WITH_SECURE_32_BYTE_HEX_JWT_SECRET/$JWT_KEY/" .env.production
sed -i "s/REPLACE_WITH_SECURE_32_BYTE_HEX_RECONCILIATION_SECRET/$RECON_KEY/" .env.production
sed -i "s/REPLACE_WITH_SECURE_32_BYTE_HEX_WEBHOOK_SECRET/$TOPUP_KEY/" .env.production
sed -i "s/REPLACE_WITH_STRONG_POSTGRES_PASSWORD/$PG_PASS/" .env.production
sed -i "s/REPLACE_WITH_SECURE_GRAFANA_ADMIN_PASSWORD/$GRAFANA_PASS/" .env.production

# Restrict file permissions strictly to owner (read/write only)
chmod 600 .env.production

# Verification: Ensure zero unresolved placeholders remain before proceeding
if grep -Eqi "(REPLACE_WITH|CHANGE_ME|<.*>)" .env.production; then
    echo "[ERROR] Unresolved placeholders detected in .env.production:"
    grep -Eoi "(REPLACE_WITH[A-Z0-9_]*|CHANGE_ME|<.*>)" .env.production
    exit 1
fi
echo "[OK] Environment file .env.production configured safely."
```

### Step 4.3: Safe Phased Deployment Workflow

#### Phase 1: Launch Core Infrastructure
Start the data stores and message broker first:
```bash
docker compose --env-file .env.production -f docker-compose.prod.yml up -d postgres redis kafka kafka-init

# Wait 15-20 seconds and verify healthchecks:
docker compose --env-file .env.production -f docker-compose.prod.yml ps
```
Ensure `postgres`, `redis`, and `kafka` report `healthy`, and `kafka-init` reports `exited (0)`.

#### Phase 2: Run Database Migration Preflight & Verification
Verify connectivity, DSN validity, and schema state across all 11 databases:
```bash
# 1. Run read-only preflight verification across all 11 databases:
docker compose --env-file .env.production -f docker-compose.prod.yml run --rm migration -service=all -action=verify -strict

# 2. Inspect migration versions (should show pending on initial deploy):
docker compose --env-file .env.production -f docker-compose.prod.yml run --rm migration -service=all -action=status -strict
```

#### Phase 3: Create Pre-Migration Database Snapshot (Crucial for Updates)
```bash
mkdir -p /home/ubuntu/backups
docker compose --env-file .env.production -f docker-compose.prod.yml exec -T postgres pg_dumpall -U tradedrift_admin | gzip > /home/ubuntu/backups/tradedrift_pre_migration_$(date +%Y%m%d_%H%M%S).sql.gz
```

#### Phase 4: Apply Database Migrations
Execute schema creation across all 11 services:
```bash
docker compose --env-file .env.production -f docker-compose.prod.yml run --rm migration -service=all -action=up -strict
```

#### Phase 5: Launch Microservices, Gateway & Monitoring
```bash
docker compose --env-file .env.production -f docker-compose.prod.yml up -d --build

# Confirm all 17 containers are healthy and running:
docker compose --env-file .env.production -f docker-compose.prod.yml ps
```

---

## 5. Nginx Reverse Proxy with TLS & WebSockets

### Prerequisites
1. Allocate an **AWS Elastic IP** and associate it with your EC2 instance.
2. In your DNS provider (e.g. Cloudflare, Route53, Namecheap), create an **`A` record** pointing your API domain (e.g., `api.yourdomain.com`) to the Elastic IP.
3. Verify DNS propagation: `dig +short api.yourdomain.com` should return your Elastic IP.

### Step 5.1: Configure Nginx Reverse Proxy
Create `/etc/nginx/sites-available/tradedrift`:

```bash
sudo tee /etc/nginx/sites-available/tradedrift << 'EOF'
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    listen [::]:80;
    server_name api.yourdomain.com;

    client_max_body_size 10M;

    # Gateway REST API Proxy
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Gateway Real-Time WebSocket Proxy (/ws)
    location /ws {
        proxy_pass http://127.0.0.1:8080/ws;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Keep alive long-lived ticker & orderbook streams
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
EOF

sudo ln -sf /etc/nginx/sites-available/tradedrift /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx
```

### Step 5.2: Issue Let's Encrypt SSL/TLS Certificate
```bash
sudo certbot --nginx -d api.yourdomain.com --non-interactive --agree-tos -m your-email@example.com

# Verify auto-renewal timer:
sudo certbot renew --dry-run
```

---

## 6. Frontend Vercel Integration

In your Vercel Dashboard for `tradedrift-frontend`:
1. Navigate to **Project Settings > Environment Variables**.
2. Update the API and WebSocket targets to point to your new secured domain:
   * `NEXT_PUBLIC_API_URL` = `https://api.yourdomain.com`
   * `NEXT_PUBLIC_WS_URL` = `wss://api.yourdomain.com/ws`
3. Trigger a redeploy of the Vercel frontend.

---

## 7. Operations, Backups & Maintenance Runbook

### Private Grafana Dashboard Access
Grafana binds strictly to `127.0.0.1:3000` to prevent public access. Access it securely via SSH port forwarding:

**From your local machine:**
```powershell
# PowerShell on Windows:
ssh -i C:\path\to\your-key.pem -L 3000:localhost:3000 ubuntu@<EC2_PUBLIC_IP>
```
Open **`http://localhost:3000`** in your browser. Log in as `admin` with `GF_SECURITY_ADMIN_PASSWORD` from `.env.production`.

### Health & Readiness Checks
```bash
# Internal Gateway healthcheck:
curl -i http://127.0.0.1:8080/health

# Public TLS healthcheck:
curl -i https://api.yourdomain.com/health
```

### Container Logs
```bash
# Stream logs for a specific service:
docker compose --env-file .env.production -f docker-compose.prod.yml logs -f gateway

# Stream logs for matching engine:
docker compose --env-file .env.production -f docker-compose.prod.yml logs -f matching-engine
```

### Automated Database Backup
Create `/home/ubuntu/backup-databases.sh`:
```bash
#!/bin/bash
BACKUP_DIR="/home/ubuntu/backups"
mkdir -p "${BACKUP_DIR}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

docker compose --env-file /home/ubuntu/tradedrift/.env.production -f /home/ubuntu/tradedrift/docker-compose.prod.yml \
  exec -T postgres pg_dumpall -U tradedrift_admin | gzip > "${BACKUP_DIR}/tradedrift_${TIMESTAMP}.sql.gz"

# Retain last 7 days of daily backups
find "${BACKUP_DIR}" -type f -name "tradedrift_*.sql.gz" -mtime +7 -delete
```
Make executable: `chmod +x /home/ubuntu/backup-databases.sh`. Add to `crontab -e` for daily 02:00 AM execution:
```cron
0 2 * * * /home/ubuntu/backup-databases.sh >> /home/ubuntu/backups/backup.log 2>&1
```

### Database Restore Procedure
```bash
# To restore from a snapshot:
gunzip -c /home/ubuntu/backups/tradedrift_<TIMESTAMP>.sql.gz | \
  docker compose --env-file .env.production -f docker-compose.prod.yml exec -T postgres psql -U tradedrift_admin -d postgres
```
