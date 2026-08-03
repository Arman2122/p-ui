Penhoon UI on Windows
=====================

Start the panel by running p-ui.exe. It has to stay running the whole time --
there is no Windows service, and no bash management menu (p-ui.sh is Linux only).

Fail2ban is not available on Windows, so the IP Limit feature cannot be used.

If you forget your password, open the panel database (p-ui.db, next to p-ui.exe)
with https://sqlitebrowser.org/

default setting:
http://localhost:2053/
user: admin
pass: admin
port: 2053

Self-signed certificate (OpenSSL installer is in the SSL folder):
openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout localhost.key -out localhost.crt
