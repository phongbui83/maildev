# MailDev lite

Phiên bản **một dự án, chạy độc lập (air-gap)** của MailDev, dành cho các site khách hàng không thể cấp PostgreSQL, Active Directory hay OpenBao. Ứng dụng của khách gửi mail (SMTP hoặc API kiểu SendGrid / SES / Azure / Graph / Gmail) vào MailDev lite; mail được **bắt lại và lưu nội bộ, không bao giờ gửi ra ngoài** (trừ khi bật relay và owner duyệt từng mail). Người dùng xem mail trên web hoặc bằng Outlook (IMAP/POP3).

| | Bản đầy đủ (`maildev.example.com`) | Bản lite |
|---|---|---|
| Triển khai | Kubernetes, 2 image (api, web) | **1 container**, `docker compose` |
| CSDL | PostgreSQL ngoài | **SQLite** trong thư mục `data/` |
| Đăng nhập | Active Directory + tài khoản khách | **Chỉ tài khoản nội bộ** (admin tự tạo) |
| Dự án | Nhiều, admin tạo | **Một dự án `default`**, mọi tài khoản tự động là member |
| Vai trò | admin / auditor / user; owner / member | Giống hệt, tài khoản nội bộ được làm owner, admin, auditor |
| Mã hoá dữ liệu nghỉ | OpenBao Transit theo dự án | Không (khuyến nghị mã hoá ổ đĩa) |
| Chứng chỉ TLS | cert-manager (Let's Encrypt) | **Cấp sẵn 10 năm từ OpenBao OpenBao PKI** khi đóng gói, hoặc tự ký |
| Giao thức | SMTP 25/587/465, IMAP 143/993, POP3 110/995, HTTPS 443, API | Giống hệt |
| Relay có duyệt, S/MIME, audit, retention, i18n vi/en/ja | Có | Có |

Mã nguồn dùng chung với bản đầy đủ (xem `CLAUDE.md`): chỉ tầng lưu trữ (SQLite) và phần nhúng giao diện là riêng.

---

## 1. Kiến trúc

```
                 ┌──────────────────────────── container maildev-lite ───────────────────────────┐
 trình duyệt ──443→ 8443 HTTPS ─┐                                                                 │
 Outlook ──143/993→ 1143/1993 ──┤  Go binary: web UI nhúng + REST API + SMTP + IMAP + POP3        │
 Outlook ──110/995→ 1110/1995 ──┤        │                                                        │
 ứng dụng ──25/587/465→ 1025/1587/1465 ──┘        ▼                                               │
                                        /data/maildev.db (SQLite, WAL)   /data/master.key          │
                                        /certs/tls.crt tls.key smime-ca.pem (mount chỉ đọc)       │
                 └───────────────────────────────────────────────────────────────────────────────┘
```

- Container chạy bằng user không đặc quyền nên lắng nghe các cổng > 1024; `docker-compose.yml` ánh xạ cổng chuẩn của host vào. Giao diện hiển thị đúng cổng chuẩn (25/587/465/143/993/110/995).
- Toàn bộ trạng thái nằm trong `data/` (CSDL + khoá chủ `master.key` dùng mã hoá credential và mật khẩu relay). **Sao lưu thư mục này là đủ.**
- Không cần Internet khi chạy: không gọi ra ngoài, không cập nhật tự động.

## 2. Yêu cầu tại site khách hàng

- Máy chủ Linux x86-64 (Ubuntu 22.04+/RHEL 8+ hoặc tương đương), 2 CPU, 2 GB RAM, 20 GB đĩa (mail bị xoá tự động sau `retention_days`, mặc định 14 ngày, tối đa 180).
- Docker Engine 24+ kèm plugin `docker compose` (hoặc Podman 4+ với `podman-compose`).
- Một tên miền nội bộ trỏ về máy chủ, ví dụ `maildev.customer.local` (DNS nội bộ hoặc file hosts). Tên này phải trùng với chứng chỉ đã cấp.
- Mở các cổng trên firewall nội bộ: 443 (web), 25/587/465 (SMTP), 143/993 (IMAP), 110/995 (POP3). Không cần mở ra Internet.

## 3. Đóng gói (phía nhà cung cấp) (máy có Internet)

```bash
git clone https://github.com/phongbui83/maildev.git
cd maildev-lite
# (tuỳ chọn) lấy code mới nhất của bản đầy đủ nếu ../backend và ../frontend đã được clone cạnh đây
make sync

# cấp chứng chỉ 10 năm từ OpenBao PKI (pki-int, role tls-any) và tạo bundle air-gap
export BAO_ADDR=https://vault.example.com BAO_TOKEN=<token có quyền pki-int/issue/tls-any>
make package HOST=maildev.customer.local IP=10.20.30.40
# -> dist/maildev-lite-<version>-maildev.customer.local.tar.gz
```

Bundle gồm:

```
bundle-maildev.customer.local/
├── maildev-lite-image-<version>.tar.gz   # image Docker (docker save)
├── docker-compose.yml
├── .env                                   # hostname, image, tài khoản admin ban đầu
├── certs/tls.crt  tls.key                 # chứng chỉ 10 năm + chuỗi CA
├── certs/ca.crt                           # CA để cài lên máy người dùng (trình duyệt, Outlook)
├── certs/smime-ca.pem                     # CA tin cậy để xác minh chữ ký S/MIME
├── install.sh
└── README.md (file này)
```

Chỉ build image (không cấp cert): `make image`. Chỉ cấp cert: `make cert HOST=... IP=...` (ghi vào `certs/`).

## 4. Cài đặt tại khách hàng (không cần Internet)

```bash
# 1. chép bundle sang máy chủ (USB, SCP...), giải nén
tar xzf maildev-lite-<version>-maildev.customer.local.tar.gz
cd bundle-maildev.customer.local

# 2. kiểm tra .env (hostname, mật khẩu admin ban đầu nếu muốn đổi) rồi cài
./install.sh          # = docker load + docker compose up -d

# 3. kiểm tra
docker compose ps
docker compose logs -f maildev      # dòng "https listening" và "smtp listening" là chạy OK
curl -k https://maildev.customer.local/readyz   # -> ready
```

Mở `https://maildev.customer.local/`, đăng nhập `admin` / `admin` (hoặc giá trị `MAILDEV_LITE_ADMIN_*` trong `.env`). Hệ thống **bắt buộc đổi mật khẩu ngay** lần đầu (tối thiểu 12 ký tự, có chữ hoa, chữ thường, số). Nút **Hướng dẫn sử dụng** ở màn hình đăng nhập mở tài liệu đầy đủ (vi/en/ja) kèm ảnh chụp từng màn hình.

Cài `certs/ca.crt` vào kho chứng chỉ tin cậy của máy người dùng (Windows: *Trusted Root Certification Authorities*) để trình duyệt và Outlook không cảnh báo. Nếu bundle không có cert (chạy thử), container tự tạo chứng chỉ tự ký cho hostname.

## 5. Cấu hình `.env`

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `MAILDEV_HOSTNAME` | `maildev.local` | Tên máy chủ người dùng kết nối; hiện trong trang *Thông tin phát triển*; dùng cho cert tự ký |
| `MAILDEV_PUBLIC_URL` | `https://<hostname>` | URL web, dùng trong hướng dẫn API |
| `MAILDEV_LITE_ADMIN_USER` / `_PASSWORD` | `admin` / `admin` | Tài khoản admin đầu tiên, chỉ tạo khi chưa có admin nào |
| `MAILDEV_LITE_PROJECT_NAME` | `Default` | Tên hiển thị của dự án duy nhất (slug cố định `default`) |
| `MAILDEV_MAX_MESSAGE_BYTES` | `26214400` | Giới hạn kích thước mail (25 MB) |
| `MAILDEV_SESSION_TTL` | `12h` | Thời gian phiên web |
| `MAILDEV_HTTP_ADDR` | `:8080` | HTTP thường (cho reverse proxy / healthcheck); `""` để tắt |
| `MAILDEV_HTTPS_ADDR` | `:8443` | HTTPS; `""` để tắt khi đã có reverse proxy TLS |
| `MAILDEV_COOKIE_SECURE` | `true` | Đặt `false` chỉ khi truy cập qua HTTP thường |
| `MAILDEV_MASTER_KEY` | tự sinh vào `/data/master.key` | Khoá chủ (base64 32 byte) mã hoá credential và mật khẩu relay |
| `MAILDEV_SMIME_CA_FILE` | `/certs/smime-ca.pem` nếu có | CA tin cậy để xác minh chữ ký S/MIME |
| `MAILDEV_LOG_MAIL_EVENTS` | `true` | Ghi log mỗi mail bắt được |

Mọi biến `MAILDEV_*` của bản đầy đủ đều được chấp nhận (ví dụ `MAILDEV_LDAP_*` với `MAILDEV_AUTH_MODE=ldap` nếu site có AD riêng).

## 6. Vận hành hằng ngày

**Tài khoản** (admin): *Quản trị → Người dùng → Tạo tài khoản*. Nhập username, tên, email và mật khẩu (hoặc để trống để hệ thống sinh mật khẩu tạm, hiện một lần). Người dùng phải đổi mật khẩu ở lần đăng nhập đầu. Admin có thể đặt vai trò toàn cục `admin` / `auditor` / `user`, vô hiệu hoá, mở khoá (khoá 15 phút sau 5 lần sai), đặt lại mật khẩu, xoá.

**Dự án**: mọi tài khoản tự động là **member** của dự án `default` khi đăng nhập; admin tự động là **owner**. Owner thêm owner khác tại *Quản lý dự án → Thành viên* (gõ username, chọn vai trò). Member đọc mail, đánh dấu đã đọc, xoá; owner thêm khởi tạo lại credential, đổi mô tả, duyệt relay.

**Kết nối ứng dụng**: *Thông tin phát triển* hiển thị 3 bộ credential (SMTP·IMAP·POP3, API key, AWS SES) và hướng dẫn kết nối theo nhà cung cấp (SMTP, Outlook/IMAP, REST, SendGrid, SES, Azure ACS/Graph, Gmail) với mã mẫu Node.js/Python/Java/.NET/PHP/Go/curl. Ứng dụng chỉ cần đổi host + credential, giữ nguyên SDK.

**Outlook / Thunderbird**: IMAP `maildev.customer.local:993` (SSL) hoặc 143 (STARTTLS), POP3 995/110, tài khoản = username/mật khẩu SMTP của dự án (hộp thư `INBOX` duy nhất). Xoá trong Outlook sẽ xoá mail khỏi dự án.

**Relay (tuỳ chọn, mặc định tắt)**: admin cấu hình đích thật (SMTP / SES / SendGrid, danh sách người nhận cho phép, from override) tại *Quản lý dự án → Relay* và bật. Từ đó mọi mail mới vào *Hàng đợi relay*; owner duyệt từng mail hoặc theo lô; mọi quyết định được ghi audit.

**Lưu trữ**: mặc định giữ 14 ngày và tối đa 5000 mail; admin đổi tại *Quản lý dự án → Cài đặt* (tối đa 180 ngày). Dọn dẹp tự động mỗi 10 phút, ghi audit `retention_cleanup`.

**Nhật ký**: admin và auditor xem *Quản trị → Logs* (toàn hệ thống, lọc theo giao thức/hành động/tác nhân, có IP); owner xem log của dự án tại *Quản lý dự án → Audit*.

## 7. Sao lưu, khôi phục, nâng cấp

```bash
# sao lưu (nên dừng để file nhất quán; hoặc dùng sqlite3 .backup khi đang chạy)
docker compose stop && tar czf maildev-backup-$(date +%F).tgz data certs .env && docker compose start

# khôi phục trên máy mới: giải nén bundle, chép data/ và certs/ đè lên, ./install.sh
# nâng cấp: nhận bundle mới từ nhà cung cấp
docker load < maildev-lite-image-<new>.tar.gz
sed -i 's/^MAILDEV_IMAGE=.*/MAILDEV_IMAGE=docker.example.com\/maildev\/lite:<new>/' .env
docker compose up -d        # schema SQLite được nâng cấp tự động khi khởi động
```

`data/master.key` bắt buộc đi cùng `data/maildev.db`: mất khoá thì credential đã lưu không đọc được (phải *Khởi tạo lại* credential của dự án).

## 8. Khắc phục sự cố

| Hiện tượng | Xử lý |
|---|---|
| `bind: address already in use` | Cổng 25/143/443… đang bị dịch vụ khác giữ (Postfix, Dovecot, nginx). Dừng dịch vụ đó hoặc sửa ánh xạ cổng trong `docker-compose.yml` và `MAILDEV_PUBLIC_*_PORTS` |
| Trình duyệt / Outlook báo chứng chỉ không tin cậy | Cài `certs/ca.crt` vào kho tin cậy; kiểm tra hostname truy cập trùng với cert (`openssl x509 -in certs/tls.crt -noout -subject -ext subjectAltName`) |
| Quên mật khẩu admin / bị khoá | `docker compose run --rm maildev reset-admin admin` → in mật khẩu tạm, bắt buộc đổi khi đăng nhập |
| Đăng nhập xong lại quay về trang login | Truy cập qua HTTP thường mà `MAILDEV_COOKIE_SECURE=true`. Dùng HTTPS, hoặc đặt `false` nếu có reverse proxy HTTP |
| Ứng dụng báo `530 Authentication required` | Phải đăng nhập SMTP bằng username/mật khẩu của dự án (trang *Thông tin phát triển*); địa chỉ người nhận không quan trọng |
| Mail không thấy trong Outlook | Outlook phải cấu hình IMAP/POP3, không phải Exchange; kiểm tra cổng 993/995 và credential SMTP của dự án |
| `database is locked` trong log | Ổ đĩa quá chậm/đầy, hoặc có tiến trình khác mở `maildev.db`. Không chạy 2 container trên cùng `data/` |

Log: `docker compose logs --tail=200 maildev` (JSON, mỗi mail bắt được là một dòng `mail captured`).

## 9. Phát triển / kiểm thử

```bash
make sync          # lấy code dùng chung từ ../backend và ../frontend (ghi UPSTREAM)
make web           # build giao diện tĩnh vào internal/webui/dist
make build         # dist/maildev-lite ; chạy: ./dist/maildev-lite  (https://localhost:8443, admin/admin)
make test          # chạy toàn bộ smoke/relay/local-user test của bản đầy đủ + lite_test.py trên binary lite
```

Giới hạn đã biết: một dự án; không AD; không mã hoá dữ liệu nghỉ (mail nằm rõ trong SQLite, hãy mã hoá ổ đĩa); không chạy nhiều bản sao (SSE/IMAP IDLE dùng bus trong tiến trình).

---
Phát triển bởi Bùi Thế Phong · Giấy phép MIT
