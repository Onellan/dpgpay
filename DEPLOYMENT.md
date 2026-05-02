# DPG Pay - Deployment Guide

This guide provides instructions for deploying DPG Pay using Docker images built by the CI pipeline.

## Prerequisites

- Docker & Docker Compose (for local deployment)
- GitHub account with access to the repository
- Docker registry account (Docker Hub, AWS ECR, or use GitHub Container Registry)
- Environment configuration file (`.env`)

## CI/CD Pipeline Overview

The GitHub Actions CI pipeline:
- ✅ Runs on every push to `main` and `develop` branches
- ✅ Runs on all pull requests
- ✅ Builds Docker images for `linux/arm64` and `linux/amd64`
- ✅ Pushes images to GitHub Container Registry (GHCR)
- ✅ Tags images with branch name, commit SHA, and `latest`
- ✅ Runs Go tests and build verification
- ✅ Caches layers for faster builds

### Image Registry

Images are automatically pushed to:
```
ghcr.io/<github-owner>/<repository-name>:<tag>
```

**Example tags:**
- `ghcr.io/myorg/dpgpay:latest` - Latest build from main
- `ghcr.io/myorg/dpgpay:main-a1b2c3d` - Specific commit SHA
- `ghcr.io/myorg/dpgpay:develop` - Latest from develop branch
- `ghcr.io/myorg/dpgpay:v1.2.3` - Semantic version (if using git tags)

## Deployment Options

### Option 1: Local Deployment with Docker Compose

**Best for:** Development, testing, or small-scale deployments

#### 1. Clone the repository
```bash
git clone https://github.com/<owner>/dpgpay.git
cd dpgpay
```

#### 2. Configure environment
```bash
cp .env.example .env
# Edit .env with your settings
nano .env
```

**Required variables:**
```env
ADMIN_USERNAME=admin
ADMIN_PASSWORD_BCRYPT=<bcrypt-hash-of-password>
ADMIN_EMAIL=admin@example.com
BASE_URL=
DB_PATH=/data/dpgpay.db
PORT=18231
CSRF_AUTH_KEY=<generate-random-key>
```

When `BASE_URL` is blank, the app derives it from the request host and forwarded proto headers, which is usually the right default in production.

#### 3. Start the service
```bash
docker compose up -d
```

#### 4. Access the application
```
http://localhost:18231
```

#### 5. View logs
```bash
docker compose logs -f dpg-pay
```

---

### Option 2: Docker Compose with Pre-built Image from GHCR

**Best for:** Quick deployments using published images

#### 1. Use the checked-in compose file

The repository already includes [docker-compose.image.yml](c:\Users\onell\OneDrive\Documents\MyGitProjects\dpgpay\docker-compose.image.yml) for the GHCR image.

It includes the same env bootstrap behavior as the source-build compose file:
- auto-creates `.env` if missing
- copies from `.env.example` when present
- otherwise generates `.env` from built-in defaults

#### 2. Authenticate to GHCR (if repository is private)
```bash
echo $CR_PAT | docker login ghcr.io -u <username> --password-stdin
```

#### 3. Pull and run
```bash
docker compose -f docker-compose.image.yml up -d
```

---

### Option 3: Amazon ECS (AWS Container Service)

**Best for:** Production deployments on AWS

#### ECS Prerequisites
- AWS account with ECR (Elastic Container Registry)
- ECS cluster configured
- AWS CLI installed

#### 1. Push image to AWS ECR
```bash
# Create ECR repository
aws ecr create-repository --repository-name dpgpay --region us-east-1

# Get login token
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin \
  <account-id>.dkr.ecr.us-east-1.amazonaws.com

# Tag and push
docker tag ghcr.io/<owner>/dpgpay:latest \
  <account-id>.dkr.ecr.us-east-1.amazonaws.com/dpgpay:latest

docker push <account-id>.dkr.ecr.us-east-1.amazonaws.com/dpgpay:latest
```

#### 2. Create ECS task definition
```json
{
  "family": "dpgpay",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "containerDefinitions": [
    {
      "name": "dpgpay",
      "image": "<account-id>.dkr.ecr.us-east-1.amazonaws.com/dpgpay:latest",
      "portMappings": [
        {
          "containerPort": 18231,
          "hostPort": 18231,
          "protocol": "tcp"
        }
      ],
      "environment": [
        {"name": "PORT", "value": "18231"},
        {"name": "BASE_URL", "value": "https://your-domain.com"}
      ],
      "secrets": [
        {"name": "ADMIN_PASSWORD_BCRYPT", "valueFrom": "arn:aws:secretsmanager:..."},
        {"name": "CSRF_AUTH_KEY", "valueFrom": "arn:aws:secretsmanager:..."}
      ],
      "mountPoints": [
        {"sourceVolume": "data", "containerPath": "/data"}
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/dpgpay",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "ecs"
        }
      }
    }
  ],
  "volumes": [
    {
      "name": "data",
      "efsVolumeConfiguration": {
        "fileSystemId": "fs-xxxxx",
        "transitEncryption": "ENABLED"
      }
    }
  ]
}
```

#### 3. Create ECS service
```bash
aws ecs create-service \
  --cluster production \
  --service-name dpgpay \
  --task-definition dpgpay:1 \
  --desired-count 2 \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[subnet-xxxxx],securityGroups=[sg-xxxxx],assignPublicIp=DISABLED}"
```

---

### Option 4: Kubernetes (K8s)

**Best for:** Multi-region, high-availability deployments

#### 1. Create namespace
```bash
kubectl create namespace dpgpay
```

#### 2. Create secret for environment
```bash
kubectl create secret generic dpgpay-env \
  --from-file=.env \
  --namespace=dpgpay
```

#### 3. Create deployment manifest (deployment.yaml)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dpgpay
  namespace: dpgpay
spec:
  replicas: 3
  selector:
    matchLabels:
      app: dpgpay
  template:
    metadata:
      labels:
        app: dpgpay
    spec:
      containers:
      - name: dpgpay
        image: ghcr.io/<owner>/dpgpay:latest
        imagePullPolicy: Always
        ports:
        - containerPort: 18231
          name: http
        envFrom:
        - secretRef:
            name: dpgpay-env
        volumeMounts:
        - name: data
          mountPath: /data
        livenessProbe:
          httpGet:
            path: /health
            port: 18231
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 18231
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: dpgpay-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: dpgpay
  namespace: dpgpay
spec:
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 18231
    protocol: TCP
  selector:
    app: dpgpay
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: dpgpay-pvc
  namespace: dpgpay
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

#### 4. Deploy
```bash
kubectl apply -f deployment.yaml
```

#### 5. Monitor
```bash
kubectl logs -f deployment/dpgpay -n dpgpay
kubectl get pods -n dpgpay
```

---

## Environment Configuration

### Essential Variables

| Variable | Description | Example |
| --- | --- | --- |
| `ADMIN_USERNAME` | Admin login username | `admin` |
| `ADMIN_PASSWORD_BCRYPT` | Bcrypt hash of admin password | `$2a$12$...` |
| `ADMIN_EMAIL` | Admin email address | `admin@example.com` |
| `BASE_URL` | Application base URL | `https://dpgpay.example.com` |
| `DB_PATH` | SQLite database path | `/data/dpgpay.db` |
| `PORT` | HTTP port | `18231` |
| `CSRF_AUTH_KEY` | CSRF token authentication key (32+ bytes) | Generate with: `openssl rand -hex 32` |

### Optional Variables

| Variable | Description | Default |
| --- | --- | --- |
| `EFT_SIMULATION_MODE` | Enable EFT payment simulation | `true` |
| `EFT_ACCOUNT_NAME` | EFT beneficiary account name | `` |
| `EFT_BANK_NAME` | EFT beneficiary bank name | `` |
| `EFT_ACCOUNT_NUMBER` | EFT beneficiary account number | `` |
| `EFT_BRANCH_CODE` | EFT beneficiary branch code | `` |
| `SMTP_HOST` | SMTP server hostname | `` |
| `SMTP_PORT` | SMTP server port | `` |
| `SMTP_USER` | SMTP username | `` |
| `SMTP_PASS` | SMTP password | `` |
| `SMTP_FROM` | Email from address | `` |
| `WEBHOOK_ENDPOINT_URL` | Webhook endpoint URL | `` |
| `WEBHOOK_SIGNING_SECRET` | Webhook signing secret | `` |

## Health Check

All deployments include a health check endpoint:

```bash
curl http://localhost:18231/health
```

Expected response: `HTTP 200 OK`

## Monitoring & Logs

### Docker Compose
```bash
# View logs
docker compose logs -f dpg-pay

# View resource usage
docker stats dpg-pay

# Execute command in container
docker compose exec dpg-pay sh
```

### Kubernetes
```bash
# View logs
kubectl logs -f deployment/dpgpay -n dpgpay

# Get pod details
kubectl describe pod <pod-name> -n dpgpay

# Port forward
kubectl port-forward svc/dpgpay 8080:80 -n dpgpay
```

## Updating to Latest Version

### Update Docker Compose
```bash
docker compose pull
docker compose up -d
```

### Update Kubernetes
```bash
kubectl rollout restart deployment/dpgpay -n dpgpay
```

## Troubleshooting

### Database Issues
```bash
# Check database file exists
ls -la /data/dpgpay.db

# Clear and reinitialize (CAUTION: Deletes all data)
rm /data/dpgpay.db
docker compose restart dpg-pay
```

### Port Already in Use
```bash
# Change port in .env
PORT=18232

# Update docker-compose.yml port mapping
ports:
  - "0.0.0.0:18232:18231"
```

### Authentication Issues
- Verify `ADMIN_USERNAME` and `ADMIN_PASSWORD_BCRYPT` are set
- Generate new bcrypt hash if needed: Use online tool or `htpasswd -Bc 10`

### CSRF Token Errors
- Ensure `CSRF_AUTH_KEY` is set and has at least 32 bytes
- Generate new key: `openssl rand -hex 32`

## Support

For issues or questions:
1. Check logs: `docker compose logs dpg-pay`
2. Verify environment configuration
3. Consult the main README.md
4. Open a GitHub issue with logs and configuration details (remove sensitive data)

---

**Last Updated:** May 2, 2026
