# collectors: stand up a new collector and attach it to a network

Four steps, each a command. Steps 1 and 4 change Forward and are dry runs until `apply: true`; step 3 is a wait.

1. **Register** (creates the collector's credential; Forward shows it once):
   `edit-platform` area `collectors` action `register`, `definition` {`name`, `env_file`}. `env_file` is a path that must not exist; it is created with mode 600 and holds one line, `TOKEN=<username>:<password>`, the form a Docker collector's `--env-file` takes. The result names the collector's id, name and username and **never the key**. Keep the file: a lost key means deleting the collector and registering again.
2. **Run the collector software on a host that can reach the devices.** Either the Docker image or the installer:
   - Docker (the pattern a working run used; the flags are from that run, not tested here): `docker run -d --name fwdcollector --network host --env-file ENV_FILE -e APP_HOST=<the Forward host, for example fwd.app> -v /var/log/fwdcollector:/collector/logs -v fwdcollector-private:/collector/private quay.io/forwardnetworks/cloud-collector:<tag>`. Pulling the image needs quay.io credentials from Forward (support or your account team). Tags are Forward release numbers; `latest` follows fwd.app. `COLLECTOR_HEAP_SIZE` sets the heap. Host networking is what lets it reach management addresses on the host's networks.
   - Installer: `fwdctl collector download --out DIR` fetches the installer for the connected Forward (`--type linux` the .sh, `windows`, `osx`); check the printed sha256, then run it on the host and give it the collector's `username:password` when it asks.
   - The key identity file (`/collector/private/customer_key.pb`) may not exist until the collector has done its first work. Keep that directory on a volume, and back it up once it exists: a rebuilt collector without it loses its identity.
3. **Wait for it:** `fwdctl wait collector NAME --timeout 10m` exits 0 on CONNECTED. If it stays NEVER_CONNECTED the collector never authenticated: read the container's log (`Authentication failed` means the TOKEN is wrong). The registration key is **already** `username:password`; do not prefix the username again.
4. **Attach:** `edit-network` object `collector` action `assign` (dry run first), then add the devices and their logins (`edit-source`). A network collects through one collector.

**Delete** (`edit-platform` area `collectors` action `delete`, `confirm` = the collector's name) cannot be undone: a new registration has a new username and key. Detach it from its networks first, and stop its container.

Limits: registration is verified by reading the collector list back, not by connecting; CONNECTED is proof only that the collector authenticated at that moment.
