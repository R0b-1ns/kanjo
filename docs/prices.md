# Prices

The estimated cost of a row comes from a price file. kanjo embeds one in the
binary (`internal/pricesfile/prices.json`); `prices_file` in the config
replaces it with your own.

## Format

A JSON object. Unknown fields are rejected. The file may not exceed 1 MiB.

| Key | Type | Required | Meaning |
|---|---|---|---|
| `source` | string | yes | URL (or text) the prices were taken from. Printed by `usage`; at most 200 characters, printable only (no control or formatting characters) |
| `fetched` | string `YYYY-MM-DD` | yes | Date the prices were taken |
| `unit` | string | no | Free text, `USD per million tokens` |
| `models` | object | yes, non-empty | Model id (same rules as the `model` field of [data-model.md](data-model.md)) mapped to its prices |

Each model entry has these keys, **all required**, numbers 0 or more, USD per
million tokens:

| Key | Meaning |
|---|---|
| `input` | Input tokens |
| `output` | Output tokens (thinking included) |
| `cache_write_5m` | Tokens written to the 5-minute cache |
| `cache_write_1h` | Tokens written to the 1-hour cache |
| `cache_read` | Tokens read from the cache |
| `long_context` | Optional second tier (object, below) |

`long_context` has `above_prompt_tokens` (integer greater than 0) plus the same
five price keys. A message is in this tier when `input + cache_read +
cache_write_5m + cache_write_1h` is **strictly greater** than
`above_prompt_tokens`. A model without `long_context` always uses its base
prices.

## Example

```json
{
  "source": "https://platform.claude.com/docs/en/about-claude/pricing",
  "fetched": "2026-11-03",
  "unit": "USD per million tokens",
  "models": {
    "claude-opus-5-5": {"input": 4, "output": 20, "cache_write_5m": 5, "cache_write_1h": 8, "cache_read": 0.20},
    "claude-haiku-5-5": {
      "input": 0.10, "output": 0.50, "cache_write_5m": 0.125, "cache_write_1h": 0.20, "cache_read": 0.01,
      "long_context": {"above_prompt_tokens": 100000, "input": 0.50, "output": 2.50, "cache_write_5m": 0.625, "cache_write_1h": 1, "cache_read": 0.05}
    }
  }
}
```

## Cost formula

```
cost = (input × p_input + output × p_output + cache_write_5m × p_5m
        + cache_write_1h × p_1h + cache_read × p_read) / 1,000,000
```

`reasoning` tokens are not charged again (Claude counts them inside `output`).
If a row has `cost_reported_usd` (set by a source that knows the real cost),
that value is used instead; 0 means free.

## Unpriced models

A model that is not in the price file has **no cost**: `est_usd` is empty for a
group made only of such models, `unpriced_messages` counts their messages, and
they add nothing to the total. They are never counted as 0.

## Updating prices

Edit the file by hand and change `fetched`. kanjo never downloads prices. For
the embedded file, change `internal/pricesfile/prices.json` and rebuild.
[`kanjo prices check`](commands/prices-check.md) validates the file, warns when
it is older than `prices.max_age_days` (default 30) and lists the models in your
data that have no price. An automatic update with a review of the diff is not
built.

The estimate is at API list prices. It is not what a subscription costs.
