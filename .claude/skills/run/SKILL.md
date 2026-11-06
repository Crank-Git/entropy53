---
name: run
description: Start a loopback entropy53 gateway and read a round through it. Use when asked to run, start or try the gateway or the CLI.
allowed-tools: Bash, Read
---

# Run entropy53

Epic 3 is the first epic with a working gateway. Before that epic, the binaries print a version only.

1. Build the binaries.

   ```
   go build -o /tmp/entropy53d ./cmd/entropy53d
   go build -o /tmp/entropy53 ./cmd/entropy53
   ```

2. Start the gateway on an unprivileged port. This step contacts the real upstreams.

   ```
   /tmp/entropy53d --zone beacon.example --ns ns1.example.net --soa-rname hostmaster.example.com. --listen 127.0.0.1:15353
   ```

3. Confirm that the gateway answers.

   ```
   dig -p 15353 @127.0.0.1 SOA beacon.example
   dig -p 15353 @127.0.0.1 TXT round.1000.quicknet.beacon.example
   ```

4. Read and verify a round with the CLI.

   ```
   /tmp/entropy53 get quicknet 1000 --zone beacon.example --resolver 127.0.0.1:15353
   ```

   The randomness for this round is `fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff3cd603d930e54fdd`.

5. Stop the gateway with SIGTERM.
