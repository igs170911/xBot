# XBot's bot computer (`make box` -> xbot-box:dev), ported from LazyBoy's box/Dockerfile + Dockerfile.
# Stages: node -> desktop (the shared Linux desktop) -> hermes (Hermes Agent at HERMES_VERSION) -> the bot image.
#
# The desktop: a real Linux desktop the human (noVNC) and the agent
# (xdotool + AT-SPI + CDP) share: Orchis-Dark + Papirus + jf open 粉圓 on XFCE 4.20,
# Traditional Chinese, Taipei time. One TigerVNC X server replaces Xvfb + x11vnc; no XFCE session manager.
# Node.js with npm, for Playwright and the bots' own scripts. The official binary carries debug symbols
# (17 MB) nobody reads here; stripping has to happen before the copy, or the image would hold both.
FROM node:22-trixie-slim AS node
COPY --chmod=755 apt-mirror /usr/local/sbin/apt-mirror
RUN --mount=type=secret,id=debian_mirror apt-mirror on && apt-get update \
    && apt-get install -y --no-install-recommends binutils && strip --strip-unneeded /usr/local/bin/node && node --version

FROM debian:trixie-slim AS desktop
ENV DEBIAN_FRONTEND=noninteractive
# Packages download from the Debian mirror scripts/build-images.sh measured fastest; see apt-mirror.
COPY --chmod=755 apt-mirror prune-icons.py /usr/local/sbin/
# dpkg never unpacks docs/man/foreign locales; zh_TW catalogs are kept so the
# XFCE menus are in Traditional Chinese. Besides the desktop, apps open files on it: a file manager, a text
# editor, an image viewer, archives and office documents (Word, Excel, PowerPoint); PDFs and web pages open in
# the browser (defaults: xfce/mimeapps.list). python3-xlib is X RECORD for box-teach-desktop. Icons: menu (16),
# panel (22), list (32) and icon-view (48) sizes, apps this computer has (prune-icons.py).
RUN --mount=type=secret,id=debian_mirror printf '%s\n' \
      'path-exclude=/usr/share/doc/*' \
      'path-include=/usr/share/doc/*/copyright' \
      'path-exclude=/usr/share/man/*' \
      'path-exclude=/usr/share/info/*' \
      'path-exclude=/usr/share/gtk-doc/*' \
      'path-exclude=/usr/share/backgrounds/*' \
      'path-exclude=/usr/share/locale/*' \
      'path-include=/usr/share/locale/locale.alias' \
      'path-include=/usr/share/locale/zh_TW/*' \
      'path-include=/usr/share/locale/zh/*' \
      > /etc/dpkg/dpkg.cfg.d/zz-xbot-slim \
    && apt-mirror on && apt-get update && apt-get install -y --no-install-recommends \
        tigervnc-standalone-server tigervnc-tools x11-utils x11-xserver-utils \
        xclip imagemagick xdotool python3-xlib \
        dbus-x11 xfconf xfce4-panel xfwm4 xfdesktop4 xfce4-settings xfce4-terminal \
        orchis-gtk-theme papirus-icon-theme adwaita-icon-theme gtk-update-icon-cache gnome-themes-extra librsvg2-common \
        at-spi2-core libatk-adaptor python3-gi gir1.2-atspi-2.0 gir1.2-gtk-3.0 \
        dconf-gsettings-backend gsettings-desktop-schemas libglib2.0-bin \
        fonts-dejavu-core fonts-liberation fonts-noto-cjk fonts-noto-color-emoji \
        locales tzdata \
        python3 curl ca-certificates git bash tini procps \
        chromium chromium-l10n \
        thunar mousepad ristretto xarchiver tumbler zip unzip 7zip \
        libreoffice-writer libreoffice-calc libreoffice-impress libreoffice-gtk3 libreoffice-l10n-zh-tw \
    && command -v thunar mousepad ristretto xarchiver soffice import 7z \
    && apt-mirror off \
    && echo "zh_TW.UTF-8 UTF-8" >> /etc/locale.gen \
    && echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen \
    && locale-gen \
    && ln -sf /usr/share/zoneinfo/Asia/Taipei /etc/localtime \
    && echo "Asia/Taipei" > /etc/timezone \
    && rm -rf /usr/share/icons/ePapirus /usr/share/icons/ePapirus-Dark /usr/share/icons/Papirus-Light \
    && /usr/local/sbin/prune-icons.py 22x22 32x32 48x48 && rm /usr/local/sbin/prune-icons.py \
    && for theme in Papirus Papirus-Dark; do gtk-update-icon-cache -f -q "/usr/share/icons/$theme"; done \
    && find /usr/share/themes -mindepth 1 -maxdepth 1 -name 'Orchis*' ! -name Orchis-Dark -exec rm -rf {} + \
    && test -e /usr/share/icons/Papirus-Dark/48x48/apps/web-browser.svg \
    && test -d /usr/share/themes/Orchis-Dark/xfwm4 \
    # Chromium's own menus in Traditional Chinese like the rest of the desktop; the other 200 languages go.
    && find /usr/lib/chromium/locales -name '*.pak' ! -name 'zh-TW*' ! -name 'en-US*' -delete \
    && test -e /usr/lib/chromium/locales/zh-TW.pak \
    # A Vulkan debugging layer Chromium never loads (it draws through SwiftShader).
    && rm -f /usr/lib/chromium/libVkLayer_khronos_validation.so /usr/lib/chromium/VkLayer_khronos_validation.json \
    # Serif CJK text falls back to Noto Sans CJK; LibreOffice's clip-art gallery and document templates go.
    && rm -f /usr/share/fonts/opentype/noto/NotoSerifCJK-*.ttc \
    && rm -rf /usr/lib/libreoffice/share/gallery /usr/lib/libreoffice/share/template \
    && rm -rf /var/lib/apt/lists/*
# noVNC's bridge, without Debian's package: that one pulls in NumPy and friends (37 MB) for a speedup of the
# small browser-to-desktop frames. uv stays: the bot image runs uvx for MCP servers.
COPY --from=ghcr.io/astral-sh/uv:0.12.23 /uv /uvx /usr/local/bin/
RUN UV_CACHE_DIR=/tmp/uv-cache UV_PYTHON_DOWNLOADS=never uv venv -q --python /usr/bin/python3 /opt/websockify \
    && UV_CACHE_DIR=/tmp/uv-cache uv pip install -q --no-deps --python /opt/websockify/bin/python websockify==0.13.0 \
    && ln -s /opt/websockify/bin/websockify /usr/local/bin/websockify && rm -rf /tmp/uv-cache \
    && websockify --help >/dev/null
# noVNC's web client is static files; Debian's novnc package would also pull in a second Node.js.
# The viewer URL LazyBoy handed out (its server/computers.py; XBot's Box Manager follows it) was written for noVNC 1.3, which took `path` from the
# site root; 1.6 takes it from the page, so mandatory.json (stronger than URL parameters) sets it here. The other
# two files only stop 1.6 asking for them.
RUN curl -fsSL https://github.com/novnc/noVNC/archive/refs/tags/v1.6.0.tar.gz -o /tmp/novnc.tgz \
    && echo '5066103959ef4e9b10f37e5a148627360dd8414e4cf8a7db92bdbd022e728aaa  /tmp/novnc.tgz' | sha256sum -c \
    && mkdir -p /usr/share/novnc \
    && tar xzf /tmp/novnc.tgz -C /usr/share/novnc --strip-components=1 noVNC-1.6.0/app noVNC-1.6.0/core noVNC-1.6.0/vendor noVNC-1.6.0/vnc.html noVNC-1.6.0/vnc_lite.html noVNC-1.6.0/package.json noVNC-1.6.0/LICENSE.txt \
    && echo '{"path": "websockify"}' > /usr/share/novnc/mandatory.json && echo '{}' > /usr/share/novnc/defaults.json \
    && rm /tmp/novnc.tgz
# jf open 粉圓 (OFL) as the desktop UI font, pinned like the web UI's.
RUN mkdir -p /usr/share/fonts/truetype/huninn \
    && curl -fsSL https://github.com/justfont/open-huninn-font/releases/download/v2.1/jf-openhuninn-2.1.ttf \
         -o /usr/share/fonts/truetype/huninn/jf-openhuninn-2.1.ttf \
    && echo '9d5bf4932d31fe94c18cd8cfddc98bc1b14ce10f4e354c682179db290a99c825  /usr/share/fonts/truetype/huninn/jf-openhuninn-2.1.ttf' | sha256sum -c \
    && fc-cache -f
# Node.js from the stage above; its C headers are left out.
COPY --from=node /usr/local/bin/ /usr/local/bin/
COPY --from=node /usr/local/lib/ /usr/local/lib/
# Tools that should track upstream go below this line. `make box-latest` passes a new
# BOX_REFRESH each time, which invalidates Docker's cache from here on so they are
# downloaded again; `make box` keeps the cache and the pinned versions.
ARG BOX_REFRESH=pinned
COPY playwright/ /opt/xbot/playwright/
RUN cd /opt/xbot/playwright && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install --omit=dev
# Grok Build CLI (`grok`): bots hand coding work to it through the grok-build
# skill. Login (`grok login`) is per bot and persists in /home/box/.grok.
ARG GROK_CLI_VERSION=1.0.52
# The installer leaves two more copies of its ~180 MB binary: root's ~/.grok (bots never run as root) and the
# compressed original; `grok` runs the unpacked bin/grok-native.
RUN npm install -g "@xai-official/grok@${GROK_CLI_VERSION}" && npm cache clean --force \
    && rm -rf /root/.grok /usr/local/lib/node_modules/@xai-official/grok/node_modules/@xai-official/grok-linux-* \
    && grok --version
# ACP adapters for the subscription CLIs (XBot's ACP driver, E12): Claude Code through
# @agentclientprotocol/claude-agent-acp (formerly Zed's @zed-industries/claude-code-acp; bundles the Claude Agent SDK
# and its Claude Code binary) and Codex CLI through @agentclientprotocol/codex-acp (formerly @zed-industries/codex-acp;
# pulls @openai/codex). Pinned; both speak ACP on stdin/stdout and are started per bot with `docker exec`. Logins are
# never baked in: the owner links one per bot (xbot.engines.link_credentials) into /opt/xbot/cred.
ARG CLAUDE_AGENT_ACP_VERSION=0.85.1
ARG CODEX_ACP_VERSION=2.1.1
RUN npm install -g "@agentclientprotocol/claude-agent-acp@${CLAUDE_AGENT_ACP_VERSION}" "@agentclientprotocol/codex-acp@${CODEX_ACP_VERSION}" \
    && npm cache clean --force \
    && command -v claude-agent-acp codex-acp
RUN mkdir -p /workspace /home/box /usr/share/xbot/xfce-skel /etc/xdg/xfce4 /usr/share/xfce4/helpers /etc/gtk-3.0 /etc/fonts/conf.d /usr/share/licenses/huninn \
    && chmod 1777 /workspace
COPY --chmod=755 start-desktop.sh keep-desktop-awake desktop-probe.py stop-browser.py stop-browser.mjs restore-browser.mjs box-chrome box-terminal box-a11y box-teach-browser.mjs box-teach-desktop /usr/local/bin/
COPY --chmod=644 welcome.html /usr/share/xbot/welcome.html
COPY --chmod=644 xfce/wallpaper.svg /usr/share/xbot/wallpaper.svg
COPY --chmod=644 xfce/xfce4-panel.xml xfce/xfwm4.xml xfce/xfce4-desktop.xml /usr/share/xbot/xfce-skel/
COPY --chmod=644 xfce/helpers.rc /etc/xdg/xfce4/helpers.rc
COPY --chmod=644 xfce/helper-browser.desktop /usr/share/xfce4/helpers/xbot-browser.desktop
COPY --chmod=644 xfce/helper-terminal.desktop /usr/share/xfce4/helpers/xbot-terminal.desktop
COPY --chmod=644 xfce/browser.desktop /usr/share/applications/xbot-browser.desktop
COPY --chmod=644 xfce/terminal.desktop /usr/share/applications/xbot-terminal.desktop
COPY --chmod=644 xfce/files.desktop /usr/share/applications/xbot-files.desktop
COPY --chmod=644 xfce/mimeapps.list /usr/share/applications/mimeapps.list
COPY --chmod=644 fonts.conf /etc/fonts/conf.d/99-xbot-cjk.conf
COPY --chmod=644 gtk3-settings.ini /etc/gtk-3.0/settings.ini
COPY --chmod=644 licenses/huninn-OFL.txt /usr/share/licenses/huninn/OFL.txt
# Hide the stock launchers so the menu shows only 瀏覽器 / 終端機 (box-chrome
# is the only supported way to start Chromium: same profile + CDP port).
RUN for f in chromium xfce4-terminal debian-xterm xterm uxterm; do \
      if [ -f "/usr/share/applications/${f}.desktop" ]; then \
        printf '\nNoDisplay=true\nHidden=true\n' >> "/usr/share/applications/${f}.desktop"; \
      fi; \
    done \
    && test -f /usr/share/locale/zh_TW/LC_MESSAGES/xfce4-panel.mo
# Traditional Chinese locale, Taipei time and AT-SPI on for every process in
# the box, including `docker exec` launches.
ENV DISPLAY=:1 \
    LANG=zh_TW.UTF-8 LC_ALL=zh_TW.UTF-8 LANGUAGE=zh_TW:zh:en TZ=Asia/Taipei \
    GTK_MODULES=atk-bridge GTK_A11Y=atspi GNOME_ACCESSIBILITY=1 NO_AT_BRIDGE=0
WORKDIR /workspace
EXPOSE 6080
LABEL xbot.box.revision=computer-use-11
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/start-desktop.sh"]

# ── Hermes Agent ─────────────────────────────────────────────────────────────
# Hermes at the commit XBot is tested with (HERMES_VERSION), fetched here instead of vendored into git. LazyBoy
# cloned it into vendor/hermes-agent on the host and left .git, tests, apps, website and .hermes out of the build
# context; the same trees are removed here. Hermes and its Python are built in this stage and copied over as one
# layer, after their debug symbols are stripped: stripping in the final image would keep both copies.
FROM desktop AS hermes
ENV UV_PYTHON_INSTALL_DIR=/opt/python UV_CACHE_DIR=/tmp/uv-cache
COPY HERMES_VERSION /opt/HERMES_VERSION
WORKDIR /opt/hermes
RUN commit=$(tr -d '[:space:]' < /opt/HERMES_VERSION) \
    && git init -q . && git remote add origin https://github.com/NousResearch/hermes-agent.git \
    && git fetch -q --depth 1 origin "$commit" && git checkout -q --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "$commit" \
    && rm -rf .git tests apps website .hermes
RUN uv sync --frozen --no-dev --python 3.14 --extra mcp --extra computer-use --extra sms --extra ddgs --extra anthropic \
    && uv cache clean
# Hermes itself stays on its lock file; the data libraries for the bot's own scripts follow
# upstream whenever BOX_REFRESH changes.
ARG BOX_REFRESH=pinned
RUN uv pip install --python /opt/hermes/.venv/bin/python openpyxl pdfplumber python-docx pandas && uv cache clean
RUN --mount=type=secret,id=debian_mirror apt-mirror on && apt-get update && apt-get install -y --no-install-recommends binutils \
    && find /opt/hermes/.venv /opt/python -name '*.so*' -type f -exec strip --strip-unneeded {} \; \
    && PYTHONPATH=/opt/hermes /opt/hermes/.venv/bin/python -c "import numpy, pandas, PIL, pillow_heif, cryptography, lxml, pdfplumber, openpyxl, docx, run_agent, tools.computer_use_tool, tui_gateway.ws"

# ── The bot image: desktop + Hermes + the transport host (hermes_host.py, port 8642) ──
FROM desktop
COPY --from=hermes /opt/python /opt/python
COPY --from=hermes /opt/hermes /opt/hermes
COPY --from=hermes /opt/HERMES_VERSION /opt/hermes/HERMES_VERSION
ENV UV_PYTHON_INSTALL_DIR=/opt/python UV_CACHE_DIR=/opt/uv-cache
RUN --mount=type=secret,id=debian_mirror mkdir -p /opt/uv-cache && chmod 1777 /opt/uv-cache && apt-mirror on && apt-get update \
    && apt-get install -y --no-install-recommends sudo && apt-mirror off && rm -rf /var/lib/apt/lists/*
ENV PATH="/opt/hermes/.venv/bin:${PATH}" HOME=/home/box HERMES_HOME=/hermes BROWSER_CDP_URL=http://127.0.0.1:9222 \
    TERMINAL_CWD=/workspace PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
# LazyBoy also baked product_mcp.py (its product tools as an MCP server) in here. XBot replaces it with the XBot
# MCP (internal/mcpserver), reached through the product socket the Box Manager mounts or forwards to
# /product-ipc/product.sock. xbot_mcp.py is a stdio bridge to it (Hermes registers it as MCP server "xbot";
# ACP engines run `xbot-mcp`); hermes_host.py relays /product and /model/* to the same socket.
ENV XBOT_PRODUCT_SOCK=/product-ipc/product.sock
# Subscription CLI logins the owner linked to this bot (EN-2b) are copied here, outside the home volume: root-owned
# folders, 0400 files owned by the bot user; ~/.claude/.credentials.json and ~/.codex/auth.json link to them.
RUN mkdir -p /product-ipc /opt/xbot/cred && chmod 755 /opt/xbot/cred
COPY hermes_host.py desktop_mcp.py /opt/xbot/
COPY --chmod=755 xbot_mcp.py /opt/xbot/xbot_mcp.py
RUN ln -s /opt/xbot/xbot_mcp.py /usr/local/bin/xbot-mcp
COPY --chmod=755 start-bot.sh /usr/local/bin/start-bot.sh
EXPOSE 8642 6080
WORKDIR /workspace
CMD ["/usr/local/bin/start-bot.sh"]
