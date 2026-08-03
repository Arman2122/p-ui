[English](/README.md) | [فارسی](/README.fa_IR.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/p-ui-dark.png">
    <img alt="Penhoon UI" src="./media/p-ui-light.png" width="320">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/Arman2122/p-ui/releases"><img src="https://img.shields.io/github/v/release/Arman2122/p-ui" alt="Release"></a>
  <a href="https://github.com/Arman2122/p-ui/actions"><img src="https://img.shields.io/github/actions/workflow/status/Arman2122/p-ui/release.yml.svg" alt="Build"></a>
  <a href="#"><img src="https://img.shields.io/github/go-mod/go-version/Arman2122/p-ui.svg" alt="Go Version"></a>
  <a href="https://www.gnu.org/licenses/gpl-3.0.en.html"><img src="https://img.shields.io/badge/license-GPL%20V3-blue.svg?longCache=true" alt="License"></a>
</p>

# ‏Penhoon UI

‏**Penhoon UI** (با نام اجرایی `p-ui`) یک پنل کنترل وب متن‌باز برای مدیریت سرورهای
[Xray-core](https://github.com/XTLS/Xray-core) است — برای استقرار، پیکربندی و نظارت بر
پروتکل‌های پراکسی و VPN، از یک VPS تکی تا استقرارهای چندنودی.

> ‏**Penhoon UI یک فورک از [3x-ui](https://github.com/MHSanaei/3x-ui) ساخته‌ی
> [MHSanaei](https://github.com/MHSanaei) است.** هر چیزی که در اینجا کار می‌کند، مدیون آن
> پروژه است. این پروژه همان مجوز GPL-3.0 و تاریخچه‌ی کامل کامیت‌های اصلی را حفظ می‌کند و
> برای دنبال‌کردن نقشه‌ی راه زیر از آن جدا می‌شود.

> [!IMPORTANT]
> این پروژه برای استفاده‌ی شخصی در نظر گرفته شده است. لطفاً از آن برای اهداف غیرقانونی یا
> در محیط تولید (production) استفاده نکنید.

## مسیر پروژه

‏Penhoon UI قصد ندارد صرفاً یک کپی از پروژه‌ی اصلی باشد. دو هدف، مسیر کار را مشخص می‌کنند:

- **پشتیبانی از چند پروتکل VPN فراتر از Xray.** هسته‌ی Xray سمت پراکسی را به‌خوبی پوشش
  می‌دهد، اما یک پنل VPN نباید به یک موتور واحد محدود بماند. برنامه این است که بک‌اندهای
  دیگر VPN — مانند WireGuard، OpenVPN و IKEv2/IPsec — از همین پنل مدیریت شوند، با یک مدل
  یکسان برای کلاینت‌ها، سهمیه‌ها، انقضا و سابسکریپشن‌ها، فارغ از اینکه کدام پروتکل سرویس
  را ارائه می‌دهد.
- **تکمیل کامل یکپارچگی با Xray.** رساندن پشتیبانی فعلی Xray به پوشش کامل قابلیت‌های
  هسته — همه‌ی اینباندها و اوتباندها، همه‌ی ترنسپورت‌ها، کنترل کامل مسیریابی و DNS، و
  ارائه‌ی پیکربندی در رابط کاربری به‌جای نیاز به ویرایش دستی JSON.

کار در جریان است و نقشه‌ی راه تغییر خواهد کرد. از issue و pull request استقبال می‌شود.

## ویژگی‌ها

- **اینباندهای چندپروتکلی** — VLESS، VMess، Trojan، Shadowsocks، WireGuard، Hysteria2،
  HTTP، SOCKS (Mixed)، Dokodemo-door / Tunnel و TUN.
- **ترنسپورت‌ها و امنیت مدرن** — TCP (Raw)، mKCP، WebSocket، gRPC، HTTPUpgrade و XHTTP،
  ایمن‌شده با TLS، XTLS و REALITY.
- **فال‌بک** — ارائه‌ی چند پروتکل روی یک پورت واحد (مثلاً VLESS و Trojan روی ۴۴۳).
- **مدیریت به‌ازای هر کلاینت** — سهمیه‌ی ترافیک، تاریخ انقضا، محدودیت IP، وضعیت آنلاینِ
  زنده، لینک‌های اشتراک‌گذاری، کدهای QR و سابسکریپشن‌ها با یک کلیک.
- **آمار ترافیک** — به‌ازای هر اینباند، هر کلاینت و هر اوتباند، همراه با کنترل بازنشانی.
- **پشتیبانی از چند نود** — مدیریت و مقیاس‌دهی روی چندین سرور از یک پنل واحد.
- **اوتباند و مسیریابی** — WARP، NordVPN، قوانین مسیریابی سفارشی، متعادل‌کننده‌های بار و
  زنجیره‌کردن پراکسی اوتباند.
- **سرور سابسکریپشن داخلی** با چندین فرمت خروجی و
  [قالب‌های صفحه‌ی سفارشی](docs/custom-subscription-templates.md).
- **ربات تلگرام** برای نظارت و مدیریت از راه دور.
- **‏RESTful API** همراه با مستندات Swagger درون‌پنل.
- **ذخیره‌سازی روی PostgreSQL** — یک بک‌اند واحد و شناخته‌شده، با پشتیبان‌گیری از طریق
  `pg_dump` و `pg_restore`.
- **یکپارچگی با Fail2ban** برای اعمال محدودیت IP به‌ازای هر کلاینت.
- **رابط کاربری انگلیسی و فارسی** با تم‌های تیره و روشن.

## شروع سریع

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

برای نصب یک نسخه‌ی مشخص، تگ آن را در انتها اضافه کنید:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) v1.0.0
```

نصب‌کننده، PostgreSQL را راه‌اندازی می‌کند (یا DSN سرور موجود شما را می‌پرسد) و یک نام
کاربری، رمز عبور و مسیر دسترسی تصادفی تولید می‌کند. پس از نصب، دستور `p-ui` را اجرا کنید
تا منوی مدیریت باز شود؛ در آنجا می‌توانید سرویس را شروع یا متوقف کنید، اطلاعات ورود خود را
ببینید یا بازنشانی کنید، گواهی‌های SSL را مدیریت کنید و کارهای دیگری انجام دهید.

### نصب بدون نظارت

نصب‌کننده به‌صورت غیرتعاملی نیز اجرا می‌شود، برای cloud-init و دیگر ابزارهای خودکارسازی.
مقدار `PUI_NONINTERACTIVE=1` را تنظیم کنید (یا بدون TTY آن را pipe کنید) تا نصب به‌صورت
سرتاسری و بدون هیچ پرسشی انجام شود، اطلاعات ورود تصادفی تولید کند و آن‌ها را در
`/etc/p-ui/install-result.env` بنویسد.

برای [user-data مربوط به cloud-init](deploy/cloud-init/) و
[یادداشت‌های Hetzner Cloud](deploy/marketplace/hetzner/) به [`deploy/`](deploy/) مراجعه کنید.

## پلتفرم‌های پشتیبانی‌شده

**سیستم‌عامل‌ها:** ‏Ubuntu نسخه‌های ۲۲.۰۴، ۲۴.۰۴ و ۲۶.۰۴، و Debian نسخه‌ی ۱۲ به بالا.
‏Penhoon UI فقط روی Linux اجرا می‌شود و به systemd، `apt` و `iptables` نیاز دارد؛
پشتیبانی از Windows، Docker و دیگر توزیع‌ها وجود ندارد.

**معماری‌ها:** `amd64` · `arm64` (aarch64) · `armv7` · `armv6` · `armv5` · `386` · `s390x` — فهرست فایل‌های منتشرشده
را در [صفحه‌ی releases](https://github.com/Arman2122/p-ui/releases) ببینید.

## پایگاه‌داده

‏Penhoon UI همه‌چیز را در **PostgreSQL** ذخیره می‌کند؛ این تنها بک‌اند پشتیبانی‌شده است.

نصب‌کننده می‌تواند PostgreSQL را از بسته‌های توزیع شما نصب کند و یک کاربر و پایگاه‌داده‌ی
اختصاصی بسازد، یا DSN سروری را که از قبل دارید بپذیرد. در هر دو حالت، رشته‌ی اتصال را در
فایل محیطی سرویس یعنی `/etc/default/p-ui` می‌نویسد:

```
PUI_DB_DSN=postgres://pui:password@127.0.0.1:5432/pui?sslmode=disable
```

مقدار `PUI_DB_DSN` **الزامی** است. اگر تنظیم نشده باشد یا معتبر نباشد، پنل هنگام راه‌اندازی
با خطایی متوقف می‌شود که نام همین متغیر و یک نمونه DSN را نشان می‌دهد — هیچ بک‌اند جایگزینی
در کار نیست.

پشتیبان‌گیری و بازیابی با `pg_dump` و `pg_restore` از بسته‌ی `postgresql-client` انجام
می‌شود: یک فایل `.dump` را از صفحه‌ی نمای کلی پنل دانلود کنید (یا بگذارید ربات تلگرام آن را
بفرستد) و برای بازیابی همان فایل را بارگذاری کنید. اگر فایل پشتیبان با نسخه‌ای جدیدتر از
PostgreSQL ساخته شده باشد که ابزارهای سرور نتوانند بخوانند، دستور `p-ui pgclient <major>`
کلاینت متناسب را نصب می‌کند.

## متغیرهای محیطی

| متغیر | توضیحات | پیش‌فرض |
| --- | --- | --- |
| `PUI_DB_DSN` | رشته‌ی اتصال PostgreSQL — **الزامی**؛ پنل بدون آن بالا نمی‌آید | — |
| `PUI_DB_MAX_OPEN_CONNS` | حداکثر اتصالات باز (استخر PostgreSQL) | — |
| `PUI_DB_MAX_IDLE_CONNS` | حداکثر اتصالات بیکار (استخر PostgreSQL) | — |
| `PUI_INIT_WEB_BASE_PATH` | مسیر اولیه‌ی URI برای پنل وب | `/` |
| `PUI_ENABLE_FAIL2BAN` | فعال‌سازی اعمال محدودیت IP مبتنی بر Fail2ban | `true` |
| `PUI_LOG_LEVEL` | سطح جزئیات لاگ (`debug`، `info`، `warning`، `error`) | `info` |
| `PUI_DEBUG` | فعال‌سازی حالت دیباگ | `false` |
| `PUI_TUNNEL_HEALTH_MONITOR` | فعال‌سازی مانیتور سلامت تونل (یک URL را بررسی می‌کند و پس از خطاهای مکرر xray را ری‌استارت می‌کند؛ ری‌استارت همه‌ی کلاینت‌ها را قطع می‌کند) | `false` |
| `PUI_TUNNEL_HEALTH_PROXY` | پراکسی‌ای که بررسی از طریق آن ارسال می‌شود؛ آن را به یک اینباند محلی xray اشاره دهید تا تونل واقعاً آزمایش شود (مثلاً `socks5://127.0.0.1:1080`). خالی بودن یعنی فقط اتصال به میزبان بررسی می‌شود | — |
| `PUI_TUNNEL_HEALTH_URL` | نشانی بررسی‌شده برای سلامت تونل | `https://www.cloudflare.com/cdn-cgi/trace` |
| `PUI_TUNNEL_HEALTH_INTERVAL` | فاصله‌ی بین بررسی‌ها | `30s` |
| `PUI_TUNNEL_HEALTH_TIMEOUT` | مهلت هر بررسی | `10s` |
| `PUI_TUNNEL_HEALTH_FAILURES` | تعداد خطاهای متوالی پیش از فعال‌شدن ری‌استارت | `3` |
| `PUI_TUNNEL_HEALTH_COOLDOWN` | حداقل تأخیر بین ری‌استارت‌های متوالی | `5m` |

## زبان‌ها

رابط کاربری پنل به دو زبان **English** و **فارسی** ارائه می‌شود.

## مشارکت

از مشارکت‌ها استقبال می‌شود. لطفاً پیش از باز کردن issue یا pull request،
[راهنمای مشارکت](/CONTRIBUTING.md) را مطالعه کنید.

## قدردانی

- ‏[3x-ui](https://github.com/MHSanaei/3x-ui) ساخته‌ی
  [MHSanaei](https://github.com/MHSanaei) (**GPL-3.0**) — پنل اصلی که Penhoon UI از آن فورک
  شده است. این پروژه بدون آن وجود نداشت.
- ‏[alireza0/x-ui](https://github.com/alireza0/x-ui) — پروژه‌ی مبنای خودِ 3x-ui.
- ‏[Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) (**GPL-3.0**) —
  قوانین مسیریابی با دامنه‌های ایرانی، امنیت و مسدودسازی تبلیغات.
- ‏[Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat)
  (**GPL-3.0**) — قوانین مسیریابی بر پایه‌ی دامنه‌ها و نشانی‌های مسدودشده در روسیه.

## مجوز

تحت مجوز [GPL-3.0](/LICENSE) منتشر شده است، همان مجوزی که پروژه‌ی اصلی 3x-ui دارد.
