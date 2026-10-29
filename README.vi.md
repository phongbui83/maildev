# MailDev

**Hộp thư bắt mail nhiều dự án cho môi trường phát triển và kiểm thử.** Ứng dụng gửi mail vào đây thay vì máy chủ SMTP thật hay SES/SendGrid/Azure/Graph/Gmail; mỗi mail được lưu vào dự án tương ứng với credential đã dùng và **không bao giờ được gửi đi đâu** trừ khi owner duyệt relay một cách tường minh. Nhóm dự án đọc hộp thư trên web hoặc trong Outlook qua IMAP/POP3.

> Bản tiếng Anh: [README.md](README.md). Ghi chú kiến trúc cho người đóng góp: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

![Hộp thư MailDev](frontend/public/guide/img/vi/08-inbox.png)

## Vì sao

Khi phát triển và kiểm thử, ứng dụng vẫn phải gửi email (xác nhận đăng ký, đặt lại mật khẩu, hoá đơn, báo cáo batch). Khi dữ liệu test được copy từ môi trường thật, người nhận là **khách hàng thật**: một lần chạy batch hay bấm nhầm là mail test rời khỏi công ty, không ai biết, không truy vết được. Mail catcher cá nhân (MailDev, Mailpit trên máy dev) không chia sẻ được với QA và PM; dùng chung một máy thì dự án này thấy mail của dự án kia.

MailDev giải quyết đúng điều đó: **mỗi dự án một hộp thư cô lập**, chọn theo credential mà ứng dụng dùng để xác thực, chỉ thành viên dự án đọc được, và mọi đăng nhập, mail bắt được, tải về, quyết định relay đều có trong nhật ký.

## Tính năng

- **Cô lập theo dự án**: SMTP bắt buộc AUTH (PLAIN/LOGIN), credential quyết định dự án. Địa chỉ người nhận không quan trọng nên mail lỡ gửi tới người thật chỉ đơn giản là bị bắt lại.
- **Giao thức**: SMTP (25 plain/STARTTLS, 587 STARTTLS, 465 TLS), IMAP (143 STARTTLS, 993 TLS) với một `INBOX` cho mỗi dự án, POP3 (110 STLS, 995 TLS). Dùng được với Outlook, Thunderbird, Apple Mail.
- **API tương thích nhà cung cấp**: giữ nguyên SDK, chỉ đổi endpoint và key. Endpoint JSON/raw chung, SendGrid v3 `mail/send`, AWS SES v1 (`SendEmail`, `SendRawEmail`) và v2 (`outbound-emails`), Azure Communication Services Email (xác minh HMAC-SHA256), Microsoft Graph `sendMail`, Gmail API `messages.send` (JSON và media upload).
- **Vai trò**: toàn cục `admin` (dự án, owner, người dùng, cài đặt, nhật ký) và `auditor` (chỉ đọc mọi nơi kèm nhật ký toàn hệ thống); theo dự án `owner` (thành viên, credential, cài đặt, duyệt relay) và `member` (đọc, đánh dấu đã đọc, xoá). Người ngoài dự án nhận 404.
- **Đăng nhập**: Active Directory qua LDAPS (`objectGUID` là khoá ổn định, phát hiện username bị tái sử dụng), chế độ chỉ tài khoản nội bộ (argon2id, khoá sau 5 lần sai, bắt buộc đổi mật khẩu), hoặc chế độ `dev` để chạy thử. Có thể nhập danh sách nhân sự để thêm thành viên trước khi họ đăng nhập lần đầu.
- **Relay có duyệt** (tuỳ chọn, theo dự án): admin cấu hình đích thật (SMTP, AWS SES hoặc SendGrid) và bật relay; mail bắt được nằm trong hàng đợi cho tới khi owner duyệt từng mail hoặc theo lô, có allow-list người nhận và ghi đè người gửi. Tất cả được ghi nhật ký.
- **Mã hoá dữ liệu nghỉ** (tuỳ chọn): mỗi dự án một khoá dữ liệu được bọc bởi khoá Transit riêng trên OpenBao/Vault; raw, nội dung và đính kèm mã hoá AES-GCM trong PostgreSQL, metadata vẫn tìm kiếm được. Xoá dự án là huỷ khoá.
- **S/MIME**: xác minh chữ ký (detached và opaque) theo chuỗi CA cấu hình; mail mã hoá được giữ nguyên để client tự giải mã.
- **Xem an toàn**: HTML được sanitize trong iframe sandbox, ảnh từ xa bị chặn mặc định, ảnh `cid:` lấy từ đính kèm đã lưu, xem ở độ rộng điện thoại/tablet/desktop, tải `.eml`. Đính kèm HTML/SVG buộc tải về.
- **Nhật ký** với tác nhân, IP nguồn, thời gian và giao thức cho mọi thao tác web, đăng nhập SMTP/IMAP/POP3, dùng API key, mail bắt được và quyết định relay. Nhật ký dự án cho owner, nhật ký toàn hệ thống cho admin và auditor.
- **Lưu trữ**: mặc định 14 ngày (admin đặt 1 đến 180 ngày và số mail tối đa), dọn dẹp mỗi 10 phút, dự án lưu trữ chỉ đọc và không bị dọn.
- **Cập nhật trực tiếp** (Server-Sent Events, IMAP IDLE), **giao diện Việt, Anh, Nhật**, giao diện sáng/tối, hướng dẫn sử dụng có ảnh minh hoạ tích hợp (`/guide/`).
- **Hai phiên bản**: bản đầy đủ (PostgreSQL, đăng nhập thư mục, manifest Kubernetes) và **bản lite**, một container với SQLite và tài khoản nội bộ cho site khách hàng air-gap (xem [`lite/`](lite/README.md)).

## Chạy nhanh (Docker Compose)

Yêu cầu: Docker 24+ kèm plugin compose.

```bash
git clone https://github.com/phongbui83/maildev.git && cd maildev
cp .env.example .env
sed -i "s#^MAILDEV_MASTER_KEY=.*#MAILDEV_MASTER_KEY=$(openssl rand -base64 32)#" .env   # hoặc sửa tay
docker compose up --build -d
```

Mở <http://localhost:3000>. `.env` mặc định chạy chế độ xác thực `dev`: đăng nhập với **username bất kỳ và mật khẩu `dev`**; user `admin` là quản trị viên. Dự án `demo` có sẵn và mọi người tự động tham gia.

| Dịch vụ | Cổng host (đổi trong `.env`) |
|---|---|
| Web UI (proxy API) | 3000 |
| API (REST + endpoint nhà cung cấp) | 8080 |
| SMTP plain/STARTTLS, submission, SMTPS | 1025, 1587, 1465 |
| IMAP, IMAPS | 1143, 1993 |
| POP3, POP3S | 1110, 1995 |

Gửi thử một mail:

```bash
# login SMTP của dự án nằm ở trang "Thông tin phát triển" (username = slug dự án)
swaks --to anyone@example.com --from app@example.com --server localhost:1025 \
      --auth-user demo --auth-password '<mật khẩu lấy từ UI>' --header "Subject: hello"
```

Không có file TLS thì máy chủ tự tạo chứng chỉ tự ký cho `MAILDEV_HOSTNAME`. Đặt `tls.crt`/`tls.key` vào `./certs` và khai `MAILDEV_TLS_CERT=/certs/tls.crt`, `MAILDEV_TLS_KEY=/certs/tls.key` để dùng chứng chỉ riêng. Chuyển sang Active Directory bằng `MAILDEV_AUTH_MODE=ldap` và các biến `MAILDEV_LDAP_*`, hoặc chỉ tài khoản mật khẩu bằng `MAILDEV_AUTH_MODE=local`.

Dừng bằng `docker compose down`; volume PostgreSQL `pgdata` giữ nguyên dữ liệu.

## Cấu trúc repo

```
backend/    Dịch vụ Go: SMTP, IMAP, POP3, REST API, adapter nhà cung cấp, store PostgreSQL, relay, S/MIME, mã hoá
frontend/   Web UI Next.js 16 / React 19 / Tailwind 4, hướng dẫn tích hợp (public/guide, sinh bởi scripts/guide)
lite/       Bản lite: store SQLite + UI tĩnh nhúng trong một binary; dùng lại backend/internal nguyên văn
deploy/     Manifest Kubernetes (Kustomize), thiết lập OpenBao Transit/AppRole, Makefile build/push/deploy
docs/       Ghi chú kiến trúc
docker-compose.yml, .env.example, Makefile   triển khai một máy và lệnh tắt cho developer
```

## Cấu hình

Mọi thiết lập là biến môi trường tiền tố `MAILDEV_`. Các biến quan trọng (đầy đủ trong [`backend/internal/config/config.go`](backend/internal/config/config.go)):

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `MAILDEV_MASTER_KEY` | bắt buộc | base64 32 byte; mã hoá credential và secret relay đã lưu |
| `MAILDEV_DATABASE_URL` | `postgres://maildev:maildev@localhost:5432/maildev?sslmode=disable` | PostgreSQL 14+ |
| `MAILDEV_AUTH_MODE` | `ldap` | `ldap`, `local` hoặc `dev` |
| `MAILDEV_ADMIN_USERS` | | username được nâng lên admin khi đăng nhập (người dùng thư mục/dev) |
| `MAILDEV_LDAP_URL`, `MAILDEV_LDAP_BASE_DN`, `MAILDEV_LDAP_UPN_SUFFIX`, `MAILDEV_LDAP_CA_FILE`, `MAILDEV_LDAP_BIND_DN`, `MAILDEV_LDAP_BIND_PASSWORD` | | đăng nhập LDAPS; tài khoản bind là tuỳ chọn, chỉ để tra cứu username |
| `MAILDEV_DIRECTORY_URL` | | danh sách JSON nhân sự (`username`, `fullname`, `email`, `department`) nhập mỗi 15 phút cho ô chọn thành viên |
| `MAILDEV_HOSTNAME`, `MAILDEV_PUBLIC_URL`, `MAILDEV_MAIL_HOST` | `maildev.local`, `http://localhost:3000`, = hostname | tên hiển thị cho người dùng, dùng trong lời chào SMTP/IMAP và chứng chỉ tự ký |
| `MAILDEV_HTTP_ADDR`, `MAILDEV_SMTP_ADDRS`, `MAILDEV_SMTPS_ADDRS`, `MAILDEV_IMAP_ADDRS`, `MAILDEV_IMAPS_ADDRS`, `MAILDEV_POP3_ADDRS`, `MAILDEV_POP3S_ADDRS` | `:8080`, `:1025`, ``, `:1143`, ``, `:1110`, `` | các listener (phân cách bằng dấu phẩy) |
| `MAILDEV_PUBLIC_*_PORTS` | | cổng hiển thị khi load balancer ánh xạ 25/587/465/143/993/110/995 vào listener |
| `MAILDEV_TLS_CERT`, `MAILDEV_TLS_KEY` | | chứng chỉ cho SMTPS/STARTTLS/IMAPS/POP3S (tự nạp lại khi đổi); tự ký nếu để trống |
| `MAILDEV_OPENBAO_ADDR`, `MAILDEV_OPENBAO_ROLE_ID`, `MAILDEV_OPENBAO_SECRET_ID` (hoặc `MAILDEV_OPENBAO_TOKEN`), `MAILDEV_OPENBAO_TRANSIT_MOUNT` | | bật mã hoá dữ liệu nghỉ qua Transit |
| `MAILDEV_SMIME_CA_URLS`, `MAILDEV_SMIME_CA_FILE`, `MAILDEV_SMIME_SYSTEM_ROOTS` | , , `true` | nguồn tin cậy S/MIME |
| `MAILDEV_MAX_MESSAGE_BYTES`, `MAILDEV_SESSION_TTL`, `MAILDEV_COOKIE_SECURE` | 25 MB, `12h`, `true` | giới hạn; đặt `MAILDEV_COOKIE_SECURE=false` khi UI chạy qua HTTP thường |
| `MAILDEV_DEMO_PROJECT` | `true` | tạo sẵn dự án `demo` tự động tham gia |
| `MAILDEV_EDITION` | `full` | `lite` bật quy tắc một dự án (xem `lite/`) |

## Gửi mail vào MailDev

Mỗi dự án có ba credential (hiện ở *Thông tin phát triển*, đổi toàn bộ bằng *Khởi tạo lại*):

| Credential | Dùng bởi |
|---|---|
| Login SMTP/IMAP/POP3 (`username` = slug dự án) | mọi thư viện SMTP, Outlook, Thunderbird |
| API key `<key id>.<secret>` | `POST /api/v1/send` (JSON hoặc raw RFC 822), SendGrid `POST /sendgrid/v3/mail/send`, Azure `POST /azure/<key id>/emails:send`, Graph `POST /graph/<key id>/v1.0/users/<bất kỳ>/sendMail`, Gmail `POST /gmail/<key id>/gmail/v1/users/<bất kỳ>/messages/send` |
| SES access key id + secret | `POST /ses` (API v1) và `POST /ses/v2/email/outbound-emails`; key id chọn dự án, không xác minh SigV4 |

Trang *Thông tin phát triển* có sẵn đoạn mã mẫu Node.js, Python, Java, .NET, PHP, Go và curl theo từng nhà cung cấp.

## Phát triển

### Yêu cầu

- Go 1.26+, Node.js 24+, PostgreSQL 14+ (hoặc service `postgres` trong compose), Python 3 cho các script kiểm thử đầu cuối (chỉ dùng thư viện chuẩn).

### Backend

```bash
cd backend
go build ./... && go vet ./...
go test ./...
export MAILDEV_AUTH_MODE=dev MAILDEV_ADMIN_USERS=admin MAILDEV_COOKIE_SECURE=false \
       MAILDEV_MASTER_KEY=$(openssl rand -base64 32) \
       MAILDEV_DATABASE_URL=postgres://maildev:maildev@localhost:5432/maildev?sslmode=disable
go run ./cmd/maildev          # HTTP :8080, SMTP :1025, IMAP :1143, POP3 :1110; schema tự migrate
```

Migration nằm ở `backend/internal/db/migrations/NNNN_name.sql`, áp dụng theo thứ tự khi khởi động và ghi vào `schema_migrations`. Thêm file mới, không sửa file đã áp dụng.

### Frontend

```bash
cd frontend
npm install
npm run dev                   # http://localhost:3000, proxy /api, /sendgrid, /ses, /azure, /graph, /gmail tới API_UPSTREAM (mặc định http://localhost:8080)
npm run typecheck && npm run build
```

Mọi chuỗi giao diện đi qua `useT().t("key")`; thêm key mới cho cả ba ngôn ngữ trong `lib/translations.ts` (`en`, `vi`, `ja`). `lib/api.ts` là wrapper fetch có kiểu, `lib/auth.tsx` là context phiên, `components/Shell.tsx` là layout sau đăng nhập, các trang dự án dùng chung `components/ProjectNav.tsx` và `lib/project.tsx`.

### Kiểm thử đầu cuối

Các script Python điều khiển backend thật qua SMTP, IMAP, POP3 và HTTP, in `PASS`/`FAIL` cho từng kiểm tra:

```bash
export MAILDEV_AUTH_MODE=dev MAILDEV_ADMIN_USERS=admin MAILDEV_COOKIE_SECURE=false MAILDEV_HOSTNAME=localhost \
  MAILDEV_HTTP_ADDR=:18080 MAILDEV_SMTP_ADDRS=:11025 MAILDEV_SMTPS_ADDRS=:11465 MAILDEV_IMAP_ADDRS=:11143 MAILDEV_IMAPS_ADDRS=:11993 \
  MAILDEV_POP3_ADDRS=:11110 MAILDEV_POP3S_ADDRS=:11995 MAILDEV_SMIME_SYSTEM_ROOTS=false \
  MAILDEV_MASTER_KEY=$(openssl rand -base64 32) MAILDEV_DATABASE_URL=postgres://maildev:maildev@localhost:5432/maildev_test?sslmode=disable
(cd backend && go run ./cmd/maildev &)
python3 -I backend/scripts/smoke_test.py        # SMTP/API, tìm kiếm, RBAC, nhật ký, IMAP/POP3, SSE, lưu trữ
python3 -I backend/scripts/relay_test.py        # relay qua đích SMTP, SES và SendGrid (do chính backend phục vụ)
python3 -I backend/scripts/local_users_test.py  # tài khoản mật khẩu, khoá, ngữ nghĩa lưu trữ
# tuỳ chọn: smime_test.py (tự sinh PKI test), encryption_test.py (cần OpenBao dev server, xem deploy/openbao/setup.sh)
```

Dùng cơ sở dữ liệu mới; các script giả định có dự án `demo` và tạo `erp-test`.

### Bản lite

`lite/` là module Go thứ hai cùng import path. Mọi thứ dưới `lite/internal` trừ `store` (SQLite) và `webui` (UI tĩnh nhúng) là bản sao nguyên văn của `backend/internal`, làm mới bằng `lite/scripts/sync-from-full.sh`; UI được build từ `frontend/` với `NEXT_OUTPUT=export`.

```bash
make lite-image                 # docker build -f lite/Dockerfile .
cd lite && make test            # chạy bộ test đầu cuối của backend trên binary lite + lite_test.py
cd lite && make web build && ./dist/maildev-lite     # https://localhost:8443, admin/admin
```

Đóng gói cho site không có Internet, chứng chỉ và vận hành: xem [`lite/README.md`](lite/README.md).

### Hướng dẫn sử dụng tích hợp

`frontend/public/guide/` được sinh tự động: `frontend/scripts/guide/gen_guide.py` và `gen_lite_guide.py` tạo HTML (ba ngôn ngữ), `shoot.mjs` / `shoot-lite.mjs` chụp ảnh có chú thích bằng Playwright từ một instance đang chạy (`GUIDE_BASE=http://localhost:3000 node shoot.mjs <thư mục> <user> <mật khẩu> <ngôn ngữ>`). Sau khi đổi giao diện hãy sinh lại thay vì sửa HTML.

### Quy ước

- Giữ `backend` và `lite` đồng bộ (chạy script sync và `cd lite && make test` sau khi sửa `backend/internal`).
- Mọi mail bắt được đi qua `internal/ingest`; mọi thao tác người dùng cần thấy được ghi nhật ký bằng `store.Audit` (không bao giờ làm hỏng thao tác chính).
- Handler không chạm trực tiếp vào driver cơ sở dữ liệu: thêm method vào store (bản lite cài lại API store cho SQLite).
- Adapter nhà cung cấp mới cần thêm tiền tố route ở `frontend/next.config.ts` (rewrites), `deploy/k8s/httproute.yaml` và `lite/cmd/maildev-lite/main.go` (`route`).

## Triển khai lên Kubernetes

`deploy/k8s` là một Kustomize base (deployment API một bản sao vì event bus nằm trong tiến trình, deployment web, `HTTPRoute` cho Gateway API, service `LoadBalancer` cho các cổng mail, `Certificate` của cert-manager, ConfigMap). `deploy/Makefile` build, push hai image và áp dụng manifest theo tag; `deploy/openbao/setup.sh` tạo mount Transit, policy và AppRole cho mã hoá dữ liệu nghỉ; `deploy/k8s/make-secrets.sh` tạo secret từ biến môi trường. Sửa hostname, registry và ConfigMap theo môi trường của bạn; xem [`deploy/README.md`](deploy/README.md).

## Lưu ý bảo mật

- Không bao giờ relay tự động. Relay theo dự án, admin cấu hình, owner duyệt, ghi nhật ký đầy đủ.
- Credential dự án hiện rõ trong UI là có chủ đích (chúng chỉ tới được catcher này); trong CSDL chúng được mã hoá AES-GCM bằng master key.
- HTML được sanitize (bluemonday) và hiển thị trong iframe `sandbox=""` với CSP chặn tài nguyên từ xa trừ khi người dùng yêu cầu.
- Chạy sau HTTPS (`MAILDEV_COOKIE_SECURE=true`) và chỉ mở các cổng SMTP/IMAP/POP3 cho mạng cần dùng.

## Giấy phép

MIT. Bản quyền (c) Bùi Thế Phong <buithephong@gmail.com>.
