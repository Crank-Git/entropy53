# Vector file

`vectors.json` holds the vectors. A vector is one real round with its expected record body.

## Schema

The file is one JSON object with one key, `vectors`. The value is an array of vector objects.

Each vector object has these fields (FR-record-21):

| Field | Type | Meaning |
|---|---|---|
| `beacon_id` | string | The beacon ID of the chain. |
| `round` | number | The round number. |
| `signature` | string | The signature in hexadecimal. |
| `previous_signature` | string | The previous signature in hexadecimal. The value is empty for a chain that has no previous signature. |
| `txt` | array of strings | The expected TXT strings of the record. |
| `randomness` | string | The expected randomness in hexadecimal. |

## Refresh

`make refresh-vectors` fetches round 1000 of each chain from `https://api.drand.sh` and rewrites the file. It writes nothing unless all three fetches succeed. No normal test runs it.
