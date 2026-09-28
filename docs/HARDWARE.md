# Hardware

What to buy for the closet, and how to set it up. Stockroom needs one computer, one barcode scanner and one sheet of labels. Everything else here is optional.

## The computer

Any computer from the last ten years that can stay on all day. Stockroom and its database need about 1 GB of memory and a few GB of disk, plus room for backups and photos. [`INSTALL.md`](INSTALL.md) lists the systems it installs on: Linux first, then macOS, then Windows through WSL.

It needs no internet for daily use. The nightly backup to Google Drive or GitHub does, so plug it into the school network if you can.

## The barcode scanner

Buy a **USB scanner that works as a keyboard**. Shops call this "USB HID" or "keyboard wedge". It needs no driver: it types each code it reads, then presses Enter, into whatever has focus. Stockroom tells a scan from typing by speed (CLAUDE.md §10), so any scanner that types fast works.

What to look for:

- **A 2D imager, not a laser.** A laser reads only one-dimensional barcodes printed on paper. An imager also reads QR codes and barcodes on a screen. That matters twice: the **Barcode** button on an item shows its code on screen when the sticker has fallen off, and some school ID cards carry a QR code.
- **It reads what your ID cards use.** Stockroom's labels are Code 128. School ID cards are usually Code 39, Code 128 or QR. Try a card on the scanner before you buy several.
- **Corded USB.** A cordless scanner's battery runs flat over a weekend, and its dock adds nothing in a closet.
- **A stand, or hands-free mode.** Students hold an item under a scanner on a stand faster than they pick one up.

About US $30 to $60 buys one that does all of this.

### Setting it up

Most scanners come set up correctly. The manual has a page of setup barcodes. Scan the ones that make it:

- send **Enter** (carriage return, CR) after each code. Without it, nothing happens after a scan.
- send **no prefix**. Some scanners send a character before the code by default.
- use the **US keyboard layout**, or your computer's layout if it differs.
- type with **no delay between characters**. A delay makes a scan read as typing.

Then test it:

1. Open a text editor and scan a label. The serial should appear, followed by a new line.
2. In Stockroom, sign in and press **Ctrl+Shift+D**, then scan a label. The diagnostic shows the gap between keys and whether it counted as a scan.
3. If real scans read as typing, raise **Scanner speed** in **Admin → Settings → Sign-out and scanner** a little at a time. The default is 50 ms. Keep it as low as works, because a fast typist can come close to a slow scanner.

## Labels

**Admin → Assets → Print labels** prints Code 128 barcode stickers on standard label sheets. It offers three sizes, named by what they go on, in US Letter (Avery 5167, 5160, 5162) and A4 (Avery L7651, L7160, L7163) versions. Any brand that matches those layouts works.

- **Matte labels.** Glossy ones reflect the light and won't scan.
- **Print at 100%**, never "fit to page", or the bars come out the wrong size.
- Short serials fit the small labels: about 9 characters on Letter and 7 on A4.

For labels on gear that gets handled a lot, clear tape over the sticker makes it last a year instead of a term.

## Where it goes

- **Counter height, standing.** Most visits take under a minute, so a stool slows the line down. Put the screen at eye level and the scanner beside the keyboard, on the side most people hold things.
- **Near the door, inside the closet.** Everything goes past it on the way out.
- **Out of direct sunlight.** Sunlight on the scanner window can stop it reading.
- **One power strip, labelled "do not switch off".** The service is set to start again after a power cut and catches up on a missed backup, but only once the power is back.

## Keep it awake

Stockroom runs all the time. Its nightly backup needs the machine awake at the backup hour (2 a.m. unless you changed it).

- **Never sleep.** Turn the display off after a few minutes if you like, but never let the computer itself sleep or hibernate. Under WSL, sleep can also make the clock drift, which `stockroom doctor` checks.
- **No screen lock.** A locked screen sends every scan into the password box. The closet door is the lock. If school policy requires a screen lock, use a separate account for the closet that has no access to anything else.
- **Automatic updates at night, not during the day**, and never on the backup hour.

## A full-screen browser

The closet computer shows only Stockroom. Open it in a browser's kiosk mode, so there's no address bar or tabs for a student to wander off into. Start it when the computer logs in.

Chrome or Chromium, on any system:

```bash
chromium --kiosk --app=http://127.0.0.1:8080
```

On macOS, Chrome's path is `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`. Under Windows with WSL, open `http://localhost:8080` in Edge with `msedge --kiosk http://localhost:8080 --edge-kiosk-type=fullscreen`.

To get out of kiosk mode, press Alt+F4 (Cmd+Q on a Mac).

## The closet camera

Optional, and off by default. A USB webcam over the door records who walks in and out. It needs your school's approval before it records anyone. See [`design/closet-camera.md`](design/closet-camera.md) for the camera, and [`INSTALL.md`](INSTALL.md) for turning it on.
