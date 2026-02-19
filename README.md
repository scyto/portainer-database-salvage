# portainer-database-salvage

Recovers data from a corrupted Portainer BoltDB database (`portainer.db`).

Portainer uses [bbolt](https://github.com/etcd-io/bbolt) (formerly BoltDB) to store its configuration. If the database becomes corrupted — due to disk errors, interrupted writes, or other issues — Portainer will panic on startup with errors like:

```
panic: freepages: failed to get all reachable pages (page 10: multiple references)
panic: assertion failed: Page expected to be: 202, but self identifies as 2477945687889634418
```

The standard `bbolt compact` tool will also panic on these databases because bbolt v1.4.x has strict page integrity checks.

# THIS APP SHOULD ONLY BE RUN AS LAST RESORT WHEN YOU HAVE NO OTHER OPTION AS IT WILL LOOSE DATA IN TABLES THAT CANNOT BE RECOVERED

## How it works

This tool uses **bbolt v1.3.x** which has less strict page assertions, allowing it to open databases that v1.4.x refuses. It then:

1. Opens the corrupt database in read-only mode
2. Iterates through every bucket and sub-bucket
3. Uses Go's `recover()` to catch panics from corrupt pages
4. Copies all accessible key/value pairs to a new, clean database
5. Reports what was recovered and what was lost

## Usage (Docker - recommended)

No Go installation required. Just mount the directory containing your `portainer.db`:

```bash
docker run --rm -v /path/to/data:/data golang:1.22 bash -c \
  "cd /data && go mod tidy && go build -o recover_bolt . && ./recover_bolt /data/portainer.db /data/portainer-recovered.db"
```

**Note:** Copy the `main.go` and `go.mod` files to the same directory as your `portainer.db` first, or mount them separately.

### Step-by-step example

```bash
# 1. Copy the tool files next to your corrupt database
cp main.go go.mod /path/to/portainer_data/

# 2. Run recovery via Docker
docker run --rm -v /path/to/portainer_data:/data -w /data golang:1.22 bash -c \
  "go mod tidy && go build -o recover_bolt . && ./recover_bolt /data/portainer.db /data/portainer-recovered.db"

# 3. Back up the corrupt file and swap in the recovered one
cd /path/to/portainer_data
cp portainer.db portainer-corrupt.db
cp portainer-recovered.db portainer.db

# 4. Restart Portainer
docker service update --force portainer_portainer
# or: docker restart portainer
```

## Usage (local Go)

Requires Go 1.21+:

```bash
go mod tidy
go build -o recover_bolt .
./recover_bolt /path/to/corrupt/portainer.db /path/to/portainer-recovered.db
```

## Example output

```
Found 40 top-level buckets to recover
Recovering bucket "endpoints" ...
  [OK] 4 keys, 0 sub-buckets
Recovering bucket "stacks" ...
  [OK] 34 keys, 0 sub-buckets
Recovering bucket "settings" ...
  [PANIC] Sub-bucket in "settings": assertion failed: Page expected to be: 202, ... (recovered 0 keys)
  [OK] 1 keys, 25 sub-buckets
...

=== Recovery Summary ===
Total keys recovered: 194
Buckets with errors:  0 / 40
Output: /data/portainer-recovered.db
```

Buckets with corrupt pages will show `[PANIC]` but recovery continues. Any data on non-corrupt pages is salvaged.

## After recovery

- Check Portainer's logs after starting with the recovered database
- Some settings may revert to defaults if their pages were corrupted
- Review your environment endpoints, stacks, and user accounts in the UI

## License

MIT
