[English](/README.md) | [فارسی](/README.fa_IR.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/p-ui-dark.png">
    <img alt="Penhoon UI" src="./media/p-ui-light.png" width="320">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/Arman2122/p-ui/releases"><img src="https://img.shields.io/github/v/release/Arman2122/p-ui?label=release" alt="Release"></a>
  <a href="https://github.com/Arman2122/p-ui/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Arman2122/p-ui/ci.yml?branch=main&label=CI" alt="CI"></a>
  <a href="https://github.com/Arman2122/p-ui/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/Arman2122/p-ui" alt="Go"></a>
  <a href="/LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0-blue" alt="License"></a>
</p>

<p align="center" dir="rtl">
  <strong>یک پنل برای همهٔ سرورهای پروکسی و VPN شما.</strong><br>
  ‏Xray-core، WireGuard کرنلی، AmneziaWG و MTProto — کلاینت‌ها، سهمیه‌ها، اشتراک‌ها و مسیریابی، همه در یک جا.
</p>

<p align="center">
  <a href="https://arman2122.github.io/p-ui/fa">مستندات</a>
  ·
  <a href="https://arman2122.github.io/p-ui/fa/docs/guide/installation">راهنمای نصب</a>
  ·
  <a href="https://arman2122.github.io/p-ui/fa/docs/reference/api">مرجع API</a>
  ·
  <a href="https://github.com/Arman2122/p-ui/releases">انتشارها</a>
  ·
  <a href="/CHANGELOG.md">تغییرات</a>
</p>

---

<div dir="rtl">

## فهرست

- [این پروژه چیست](#این-پروژه-چیست)
- [نصب](#نصب)
- [مدیریت پنل](#مدیریت-پنل)
- [به‌روزرسانی و حذف](#بهروزرسانی-و-حذف)
- [امکانات](#امکانات)
- [پیکربندی](#پیکربندی)
- [پایگاه‌داده](#پایگاهداده)
- [پلتفرم‌های پشتیبانی‌شده](#پلتفرمهای-پشتیبانیشده)
- [مستندات](#مستندات)
- [مشارکت](#مشارکت)
- [قدردانی](#قدردانی)
- [مجوز](#مجوز)

## این پروژه چیست

**Penhoon UI** (‏`p-ui`) یک پنل وب خودمیزبان برای راه‌اندازی سرورهای پروکسی و VPN روی لینوکس است.
این پنل [Xray-core](https://github.com/XTLS/Xray-core) را مدیریت می‌کند و در کنار آن
**WireGuard کرنلی**، **AmneziaWG** و **MTProto** را — بنابراین یک ورودی VLESS، یک تونل WireGuard و یک پروکسی
تلگرام از همان صفحه‌ها و با همان کلاینت‌ها، سهمیه‌ها، تاریخ انقضا و لینک‌های اشتراک مدیریت می‌شوند.

همه‌چیز در **PostgreSQL** ذخیره می‌شود، سرویس زیر **systemd** اجرا می‌شود و چند سرور می‌توانند به یک
استقرار واحد بپیوندند: یک پنل اصلی (master) با نودهای زیرمجموعه. رابط کاربری به انگلیسی و فارسی و در دو
پوستهٔ تیره و روشن در دسترس است.

> [!NOTE]
> این نرم‌افزار را روی سرورهایی اجرا کنید که مالک یا مدیر آن‌ها هستید و قوانین حاکم بر خود و کاربرانتان را رعایت کنید.

## نصب

### پیش‌نیازها

| | |
| --- | --- |
| **سیستم‌عامل** | Ubuntu 22.04، 24.04 یا 26.04، یا Debian 12 و جدیدتر. توزیع‌های دیگر خانوادهٔ Debian با یک هشدار نصب می‌شوند. |
| **دسترسی** | کاربر `root` روی سروری با IP عمومی. وجود `systemd` و `apt` لازم است. |
| **پایگاه‌داده** | PostgreSQL. نصاب آن را برایتان راه می‌اندازد، یا می‌توانید آدرس سروری را که از قبل دارید بدهید. |

### نصب با یک دستور

این دستور را با کاربر `root` اجرا کنید:

</div>

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

<div dir="rtl">

نصاب:

1. سرویس و وابستگی‌های آن را نصب می‌کند (از جمله `nftables` و `fail2ban`)؛
2. PostgreSQL را نصب و یک نقش و پایگاه‌دادهٔ اختصاصی می‌سازد، یا DSN سروری را که از قبل دارید می‌پرسد؛
3. یک **نام کاربری**، **رمز عبور** و **مسیر پایهٔ وب** تصادفی می‌سازد؛
4. در پایان آن‌ها را همراه با **آدرس دسترسی (Access URL)** چاپ می‌کند.

آدرس دسترسی را در مرورگر باز کنید و وارد شوید. بعداً `p-ui settings` پورت و مسیر پایه را نشان می‌دهد؛ اگر
اطلاعات ورود را گم کردید، از منوی `p-ui` گزینهٔ *Reset Username & Password* اطلاعات تازه می‌سازد.

### نصب یک نسخهٔ مشخص

برچسب انتشار را به انتهای دستور اضافه کنید:

</div>

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) v1.5.2
```

<div dir="rtl">

### نصب بدون تعامل

برای cloud-init و دیگر ابزارهای خودکارسازی، متغیر `PUI_NONINTERACTIVE=1` را تنظیم کنید (یا اسکریپت را بدون
TTY اجرا کنید). نصب بدون هیچ پرسشی تا انتها انجام می‌شود و اطلاعات ورودِ ساخته‌شده در
`/etc/p-ui/install-result.env` نوشته می‌شود:

</div>

```bash
PUI_NONINTERACTIVE=1 bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

<div dir="rtl">

نمونهٔ آمادهٔ [cloud-init user-data](deploy/cloud-init/) و
[یادداشت‌های Hetzner Cloud](deploy/marketplace/hetzner/) در پوشهٔ [`deploy/`](deploy/) قرار دارند.

### پس از نخستین ورود

[راهنمای نخستین ورود](https://arman2122.github.io/p-ui/fa/docs/guide/first-login) تنظیمات پنل، SSL و ساخت
نخستین ورودی و کلاینت را قدم‌به‌قدم توضیح می‌دهد. پورت‌ها و قوانین فایروال در
[مرجع پورت‌ها](https://arman2122.github.io/p-ui/fa/docs/reference/ports-firewall) آمده‌اند.

## مدیریت پنل

دستور `p-ui` یک منوی تعاملی باز می‌کند: نصب، به‌روزرسانی، اطلاعات ورود، گواهی‌های SSL، فایروال، PostgreSQL،
لاگ‌ها، BBR و بیشتر. هر گزینه به‌صورت زیردستور هم در دسترس است:

| دستور | کار آن |
| --- | --- |
| `p-ui start` / `stop` / `restart` | کنترل سرویس |
| `p-ui restart-xray` | راه‌اندازی مجدد فقط Xray-core |
| `p-ui status` | وضعیت سرویس |
| `p-ui settings` | نمایش پورت، مسیر پایه و وضعیت SSL |
| `p-ui enable` / `disable` | اجرای خودکار هنگام بوت، یا غیرفعال کردن آن |
| `p-ui log` | دنبال کردن لاگ پنل |
| `p-ui banlog` | لاگ مسدودسازی‌های Fail2ban |
| `p-ui update` | به‌روزرسانی به آخرین نسخهٔ پایدار |
| `p-ui update-dev` | به‌روزرسانی به بیلد در حال توسعهٔ `dev-latest` |
| `p-ui legacy` | نصب یک نسخهٔ قدیمی‌تر مشخص |
| `p-ui update-all-geofiles` | به‌روزرسانی فایل‌های geo و راه‌اندازی مجدد در صورت تغییر |
| `p-ui pgclient <major>` | نصب `postgresql-client` هم‌نسخه |
| `p-ui uninstall` | حذف پنل |

## به‌روزرسانی و حذف

- **`p-ui update`** آخرین انتشار را دانلود می‌کند، آرشیو را با checksum منتشرشده تأیید می‌کند و سرویس را دوباره
  راه می‌اندازد. پایگاه‌داده و تنظیمات دست‌نخورده می‌مانند.
- **`p-ui update-dev`** به `dev-latest` می‌رود؛ یک پیش‌انتشار به‌ازای هر کامیت — برای آزمودن یک اصلاح پیش از
  انتشار مفید است، نه برای سروری که به آن تکیه می‌کنید.
- **`p-ui legacy`** یک انتشار قدیمی‌تر را با برچسب آن نصب می‌کند.
- **`p-ui uninstall`** سرویس و فایل‌های آن را حذف می‌کند.

## امکانات

### هسته‌ها و پروتکل‌ها

- **Xray-core** — ورودی‌های VLESS، VMess، Trojan، Shadowsocks، Hysteria، HTTP، SOCKS/mixed، WireGuard،
  Dokodemo-door (تونل) و TUN. ترنسپورت‌های TCP، mKCP، WebSocket، gRPC، HTTPUpgrade و XHTTP، با امنیت TLS،
  XTLS-Vision یا REALITY. با Fallback می‌توان چند پروتکل را روی یک پورت سرو کرد.
- **WireGuard و AmneziaWG کرنلی** — تونل‌های بومی درون کرنل. ماژول AmneziaWG توسط نصاب ساخته و در DKMS
  ثبت می‌شود و طرح‌های سرعت هر کلاینت را خودِ کرنل اعمال می‌کند.
- **MTProto** — پروکسی‌های تلگرام با secret اختصاصی FakeTLS، تگ تبلیغاتی، سهمیه و انقضا برای هر کلاینت، که
  توسط `mtg-proxy` همراه پنل سرو می‌شوند.

### کلاینت‌ها و اشتراک‌ها

- سهمیهٔ ترافیک، تاریخ انقضا، محدودیت IP با اعمال از طریق Fail2ban، وضعیت آنلاین زنده و آمار به تفکیک کلاینت،
  ورودی و خروجی با امکان بازنشانی.
- یک کلاینت روی چند ورودی، با لینک اشتراک‌گذاری، QR code و سرور اشتراک داخلی که در قالب‌های raw، JSON و
  Clash پاسخ می‌دهد، همراه با [قالب‌های سفارشی صفحه](docs/custom-subscription-templates.md).

### شبکه

- خروجی‌ها و مسیریابی: قوانین مسیریابی، balancer، زنجیره‌سازی خروجی، WARP و تونل‌های خروجی WireGuard یا
  AmneziaWG که ورودی‌های انتخابی را از طریق سروری دیگر بیرون می‌برند.
- کنترل DNS و خروجی مسیریابی‌شده با policy برای تونل‌های کرنلی.

### عملیات

- **چندنودی** — یک پنل اصلی، نودهای زیرمجموعه را از طریق mTLS مدیریت می‌کند.
- **ربات تلگرام**، **هشدار ایمیلی** و همگام‌سازی کاربران از **LDAP**.
- **REST API** با سند OpenAPI در مسیر `/panel/api/openapi.json`، به‌علاوهٔ یک فید WebSocket برای وضعیت زنده.
- **پشتیبان‌گیری** با `pg_dump` / `pg_restore`، از داخل پنل یا از طریق ربات.
- یک **پایشگر سلامت تونل** اختیاری که پس از چند شکست متوالی در probe، Xray را دوباره راه می‌اندازد.

## پیکربندی

| مسیر | محتوا |
| --- | --- |
| `/usr/local/p-ui/` | خودِ پنل، اسکریپت `p-ui.sh` و پوشهٔ `bin/` شامل Xray-core و فایل‌های geo |
| `/etc/default/p-ui` | فایل محیطی سرویس — `PUI_DB_DSN` و هر متغیر دیگری از جدول زیر |
| `/etc/p-ui/install-result.env` | اطلاعات ورودی که نصب بدون تعامل می‌نویسد |
| `/var/log/p-ui/` | لاگ‌ها |

پنل گزینه‌هایش را از متغیرهای محیطی می‌خواند. آن‌ها را در `/etc/default/p-ui` بنویسید و سرویس را دوباره
راه‌اندازی کنید.

| متغیر | پیش‌فرض | توضیح |
| --- | --- | --- |
| `PUI_DB_DSN` | — | **الزامی.** رشتهٔ اتصال PostgreSQL، مثلاً `postgres://pui:password@127.0.0.1:5432/pui?sslmode=disable`. بدون آن پنل اجرا نمی‌شود. |
| `PUI_PORT` | — | جایگزینی پورت پنل؛ بر تنظیم ذخیره‌شده اولویت دارد |
| `PUI_INIT_WEB_BASE_PATH` | `/` | مسیر پایهٔ وب در نخستین اجرا |
| `PUI_ENABLE_FAIL2BAN` | `true` | اعمال محدودیت IP هر کلاینت از طریق Fail2ban |
| `PUI_SKIP_HSTS` | `false` | حذف هدر HSTS وقتی TLS در یک reverse proxy خاتمه می‌یابد |
| `PUI_LOG_LEVEL` | `info` | یکی از `debug`، `info`، `notice`، `warning` یا `error` |
| `PUI_DB_MAX_OPEN_CONNS` / `PUI_DB_MAX_IDLE_CONNS` | `25` | محدودیت‌های استخر اتصال PostgreSQL |

فهرست کامل — پوشه‌های لاگ، تنظیم حافظه، پایشگر سلامت تونل و متغیرهای خودِ نصاب — در
[مرجع متغیرهای محیطی](https://arman2122.github.io/p-ui/fa/docs/reference/env-vars) آمده است.

## پایگاه‌داده

PostgreSQL تنها پشتیبان ذخیره‌سازی است. نصاب یا آن را از بسته‌های توزیع شما نصب می‌کند و یک نقش و پایگاه‌دادهٔ
اختصاصی می‌سازد، یا DSN سروری را که از قبل دارید می‌گیرد. در هر دو حالت رشتهٔ اتصال در
`/etc/default/p-ui` قرار می‌گیرد.

پشتیبان‌گیری و بازیابی با `pg_dump` / `pg_restore` انجام می‌شود: یک فایل `.dump` از صفحهٔ نمای کلی پنل دانلود
کنید (یا بگذارید ربات تلگرام آن را بفرستد) و برای بازیابی همان را بارگذاری کنید. اگر فایل پشتیبان از PostgreSQL
جدیدتری آمده باشد که ابزارهای کلاینت شما نمی‌توانند بخوانند، `p-ui pgclient <major>` کلاینت هم‌نسخه را نصب
می‌کند.

## پلتفرم‌های پشتیبانی‌شده

- **سیستم‌عامل‌ها:** Ubuntu 22.04، 24.04 و 26.04؛ Debian 12 و جدیدتر. فقط لینوکس — وجود systemd، `apt`،
  `iptables` و `nftables` لازم است. تصویر Docker و بیلد ویندوز وجود ندارد.
- **معماری‌ها:** `amd64`، `arm64`، `armv7`، `armv6`، `armv5`، `386` و `s390x`. هر انتشار برای هر معماری یک
  آرشیو به‌همراه فایل `.sha256` آن منتشر می‌کند.

## مستندات

[سایت مستندات](https://arman2122.github.io/p-ui/fa) این بخش‌ها را پوشش می‌دهد:

- **راهنما** — [نصب](https://arman2122.github.io/p-ui/fa/docs/guide/installation)،
  [نخستین ورود](https://arman2122.github.io/p-ui/fa/docs/guide/first-login)،
  [به‌روزرسانی و حذف](https://arman2122.github.io/p-ui/fa/docs/guide/update-uninstall).
- **پیکربندی** — تنظیمات پنل، ورودی‌ها، ترنسپورت‌ها، REALITY، کلاینت‌ها، لینک‌های اشتراک‌گذاری، اشتراک‌ها و
  گواهی‌های SSL.
- **عملیات** — پشتیبان‌گیری و بازیابی، چندنودی، خروجی‌ها و مسیریابی، reverse proxy، امنیت و ربات تلگرام.
- **مرجع** — پایگاه‌داده، متغیرهای محیطی، پورت‌ها و فایروال، و
  [مرجع API](https://arman2122.github.io/p-ui/fa/docs/reference/api) که از همان سند OpenAPI پنل ساخته می‌شود.
- **کمک** — پرسش‌های متداول، مهاجرت و عیب‌یابی.

## مشارکت

از گزارش مشکل و pull request استقبال می‌شود. [CONTRIBUTING.md](/CONTRIBUTING.md) راه‌اندازی محیط، فلسفهٔ
تست و گیت محلی (‏`make verify`) را توضیح می‌دهد و [docs/architecture.md](/docs/architecture.md) نقشهٔ کد
است.

## قدردانی

- ‏Penhoon UI به‌عنوان یک فورک از [3x-ui](https://github.com/MHSanaei/3x-ui) ساختهٔ MHSanaei آغاز شد و مجوز
  GPL-3.0 آن را حفظ کرده است. از آن زمان حول چند هسته، PostgreSQL و یک فرانت‌اند جدید بازسازی شده است.
- ‏[Xray-core](https://github.com/XTLS/Xray-core) — موتور پروکسی.
- ‏[WireGuard](https://www.wireguard.com/) و
  [AmneziaWG](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module) — تونل‌های کرنلی.
- ‏[mtg](https://github.com/9seconds/mtg) — پروکسی MTProto که [`mtg-proxy`](https://github.com/Arman2122/mtg-proxy)
  بر پایهٔ آن ساخته شده است.
- ‏[Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) و
  [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat) (‏GPL-3.0) — مجموعه‌قوانین مسیریابی.

## مجوز

[GPL-3.0](/LICENSE).

</div>
