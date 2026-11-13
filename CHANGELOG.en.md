# Changelog

The important changes in osat, in English. Versions before 0.4.0 are only in the Spanish [CHANGELOG.md](CHANGELOG.md).

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [1.1.1-beta.1] - 2026-11-13

Beta version: it brings changes that are still being tested.

### Changed

- **On Windows, osat installs and updates without asking for administrator permissions**: the installer puts it in your user folder (`%LOCALAPPDATA%\Programs\OSAT`). If you had it installed in Program Files, the next update asks for permissions one last time; the one after that moves to your user folder and from then on it doesn't ask again.

## [1.1.0] - 2026-11-13

### Added

- **Source control in the Repository tab**, like VS Code's git panel: modified, new and deleted files are marked in the tree; in the new panel you see what's staged and what changed, commit, pull and push, and switch or create branches. It works with the server's git, over the same SSH connection.
- **See a file's changes** before committing, side by side or unified.
- **Repository status bar**: the branch, the commits not pushed and to pull, and where the remote is (GitHub, GitLab, Bitbucket or other).
- **Get beta versions** (Settings → General): if you turn it on, osat also offers you betas, besides the stable versions.

## [1.1.0-beta.1] - 2026-11-12

Beta version: it brings new features that are still being tested.

### Added

- **Source control in the Repository tab**, like VS Code's git panel: modified, new and deleted files are marked in the tree; in the new panel you see what's staged and what changed, commit, pull and push, and switch or create branches. It works with the server's git, over the same SSH connection.
- **See a file's changes** before committing, side by side or unified.
- **Repository status bar**: the branch, the commits not pushed and to pull, and where the remote is (GitHub, GitLab, Bitbucket or other).
- **Get beta versions** (Settings → General): if you turn it on, osat also offers you betas like this one, besides the stable versions.

## [1.0.0] - 2026-11-12

The first stable version of osat.

### What it brings together

- **SSH terminal, SFTP files and remote desktop (RDP and VNC)** in tiled tabs, also through jump hosts and an RD Gateway.
- **Websites**: your admin panels saved next to your servers, with your login kept.
- **Snippets, broadcast, port forwarding and Kubernetes**, plus importing from PuTTY, WinSCP, mRemoteNG and OpenSSH.
- **Security**: passwords in the system keyring, a password lock or Windows Hello.
- **Updates itself** and shows you what's new in each version, in English or Spanish.

### Changed

- Refreshed website and README.

## [0.5.3] - 2026-11-12

### Changed

- A test release of the updater: nothing changes in osat. It's there to see the new-version notice full screen and in your language.

## [0.5.2] - 2026-11-12

### Changed

- The update notice and the **what's new** notes show up in the language you use osat in (English or Spanish).
- The update notice and its progress use the whole osat window instead of a floating card.

## [0.5.1] - 2026-11-12

### Changed

- The **what's new** notes of a new version open in an osat tab, after your tabs from last time are restored, instead of a floating window.

## [0.5.0] - 2026-11-12

### Added

- **Automatic updates**: when it opens, osat checks whether there's a new version. If there is, it shows you what's in it and, with *Update now*, downloads it with a progress bar, closes and opens again already updated. Open connections close when it restarts. On Windows, if osat is installed in Program Files, Windows asks for permission to install.
- In Settings → General, *Check for updates when osat opens* (on by default) turns the automatic check off, and the *Check for updates* button checks right away. osat only looks at the versions published on GitHub: it doesn't send any of your data.
- **What's new**: the first time you open a new version, osat shows you what changed in a tab (like this one).

## [0.4.0] - 2026-11-11

### Added

- **Save as TXT…** in a terminal tab's menu (and in the terminal's own menu, if right-click opens a menu): saves everything the terminal holds in memory to a `.txt` file, without colors.
- **Android (beta)**: SSH terminal and SFTP in a one-screen-at-a-time interface. For now it's built from source (see `android/README.md`).
- **Default credentials**: in Settings → *Default credentials* you set a username, a password (stored in the system's credential store) and an RDP domain. Connections with *Use default credentials* use them (a checkbox in the editor, or right-click a group to tick all its connections, marked with a key in the sidebar), and so do connections missing their own username or password. Changing the password there changes it for all of them. When exporting a .osat, *Include the default credentials* (off by default) adds them with their password; when importing, you choose whether they replace this osat's.
- **Export snippets and websites in the .osat**: when exporting, *Include the snippets* and *Include the websites* (ticked, with the count) add all snippets and sites, and you can export only those, without connections. When importing, the summary says how many the file has and they're imported even if no connection is picked (the ones you already have aren't duplicated).
- **Linux package**: `build/linux/empaquetar.sh` builds `osat-linux.tar.gz` with the program, the icon and `instalar.sh`, which installs it for the user (no `sudo`) with an entry in the applications menu.
- **VNC remote desktop** in a tab, with noVNC: *VNC* protocol in the connection editor (host, port 5900 and password, stored in the system's credential store). Reached directly or *through* an SSH connection (*Proxy Jump*); to go through the server's own SSH, pick its SSH connection and use `localhost` as the host. The image scales to the tab, there's a text clipboard (Windows only, as with RDP), a *Ctrl+Alt+Del* button and a notice with *Reconnect* if it drops. Only servers with no authentication or a VNC password are supported; with VeNCrypt/TLS, RealVNC or Apple the tab says so.
- **Sites** section in the sidebar, above Workspaces: saved web pages that open in a tab with the native browser (WebView2 on Windows, WebKitGTK on Linux), keeping your login between restarts. Add them with **+** (name and address), edit or remove them with right-click, reorder by dragging and filter with the search. The section's globe, the **+** in a site's bar or `Ctrl+Shift+T` open an empty browser tab to type any address. The section folds and remembers its state. In Settings → General, *Websites* turns it off (closing open sites) and back on without restarting.
- Remote desktop through an **RD Gateway**: *RD Gateway* field in the RDP connection (for example `rds.company.com`, or `host:port`). It uses the same username, domain and password as the computer, with NTLM (no Kerberos). If the gateway asks you to approve the sign-in on your phone, the tab says so and waits up to 90 seconds; there's a single approval per connection (two the first time, if the computer's certificate has to be accepted). It can't be combined with Proxy Jump. The certificate notice says when it's the gateway's, and if the gateway rejects the password it asks again.
- Remote desktop: TLS 1.2 is used with the computer even if it offers 1.3. Windows 11 dropped the session with "internal error" before authentication when TLS 1.3 was negotiated. The log records which side ends a session, with what error and how much data went through.
- Importing from mRemoteNG brings each RDP connection's RD Gateway (also the one inherited from its folder). If it's set to "detect" or uses other credentials, it's imported anyway with a warning.
- **Password lock**: in Settings → Security you turn on a password osat asks for when it opens and after the chosen idle minutes (0 = never). While osat is locked no connection opens (not even *Restore session on open*) and the ones already open keep running behind it. On Windows you can also unlock with Windows Hello, if the PC has it; its prompt appears only once osat is in front (if it locked minimized or behind another app, it waits until you come back to the window). It's a lock screen: it doesn't encrypt the credentials and, if you forget the password, osat won't open.

### Changed

- The Claude Code bridge is set up in Settings → **Claude MCP**; there's no sidebar button anymore.
- The SFTP browser bar is icons only and, in a narrow pane, rearranges itself instead of getting cut off.
- The transfers panel collapses on its own 2 seconds after everything finished fine.
- Go dependencies are no longer copied into the repository (`vendor/`): they're downloaded when building.
