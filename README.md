# Asterisk Dashboard (Go & WebSocket)

A high-performance Golang web application that connects to an Asterisk server via AMI (Asterisk Manager Interface) and streams real-time PBX metrics to a responsive dark-themed dashboard over **WebSockets**:

- **Real-Time WebSocket Streaming:** No periodic polling delay. Asterisk AMI events instantly push state updates directly to connected web browsers.
- **Active Calls & Channels:** Live channel names, Caller ID, connected line, running application, and active call duration.
- **Call History:** Real-time tracked call history for calls made during application runtime (start time, caller ID, destination, disposition status, total duration, billable duration, and channel).
- **PJSIP Endpoints:** Endpoint IDs, live device state (`Not in use`, `In use`, `Unavailable`), context, and negotiated codecs.
- **PJSIP Contacts:** Live registered SIP contacts, URIs, availability status, and RTT roundtrip latency.
- **Trunks & Outbound Registrations:** PJSIP trunk statuses and SIP provider registrations.
- **Global States:** Asterisk version, system uptime, active calls and channel counts.

---

## 1. Asterisk AMI Configuration (`/etc/asterisk/manager.conf`)

Ensure AMI is enabled on Asterisk:

```ini
[general]
enabled = yes
port = 5038
bindaddr = 0.0.0.0

[admin]
secret = admin
read = system,call,log,verbose,command,agent,user,config
write = system,call,log,verbose,command,agent,user,config
```

Reload AMI:
```bash
asterisk -rx "manager reload"
```

---

## 2. Running the Application

### Using the Binary:
```bash
./asterisk-dashboard
```

### Using Custom CLI Options:
```bash
./asterisk-dashboard -ami-host localhost -ami-port 5038 -ami-user admin -ami-pass admin -http 8080 -auth-user admin -auth-pass secret
```

### Using Environment Variables:
```bash
export AMI_HOST=localhost
export AMI_PORT=5038
export AMI_USER=admin
export AMI_PASS=admin
export HTTP_PORT=8080
export AUTH_USER=admin
export AUTH_PASS=admin

./asterisk-dashboard
```

### Using Docker Compose:
```bash
docker compose up -d --build
```

### Using Docker:
```bash
docker build -t asterisk-dashboard .

docker run -d -p 8080:8080 \
  -e AMI_HOST=localhost \
  -e AMI_PORT=5038 \
  -e AMI_USER=admin \
  -e AMI_PASS=admin \
  -e AUTH_USER=admin \
  -e AUTH_PASS=admin \
  --name asterisk-dashboard \
  asterisk-dashboard
```

Open your browser at: **http://localhost:8080**

---

## Credits

This project and its codebase were written by **Gemini 3.8 Flash**.

---

## License

This project is licensed under the [MIT License](LICENSE).

