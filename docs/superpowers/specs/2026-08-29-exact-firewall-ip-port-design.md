# Exact Firewall IPv4 and Destination Port Design

## Goal

Extend the experimental modern firewall-policy create flow with the minimum
safe official-API capability for one exact source IPv4 address, one exact
destination IPv4 address, one exact TCP destination port, action `allow`, and
return traffic enabled between two existing exact firewall zones.

## Command contract

Add these create-only flags:

- `--source-ip`
- `--destination-ip`
- `--destination-port`

The existing zone-only create flow remains valid when none of these flags is
present. Exact-filter mode requires all three flags together and requires:

- `--action allow`
- `--allow-return-traffic`
- `--ip-version ipv4`
- `--protocol tcp`

The address flags accept only canonical literal IPv4 addresses. They reject
IPv6 addresses, CIDR subnets, address ranges, hostnames, matching-list names,
`any`, multiple addresses, and surrounding whitespace. The destination port
must be an integer from 1 through 65535.

No update flags are added. Existing updates continue to preserve the complete
official writable document, including traffic filters that the update does
not change.

## Official API representation

The source endpoint contains its resolved immutable zone ID and this exact
traffic filter:

```json
{
  "type": "IP_ADDRESS",
  "ipAddressFilter": {
    "type": "IP_ADDRESSES",
    "matchOpposite": false,
    "items": [{"type": "IP_ADDRESS", "value": "192.0.2.10"}]
  }
}
```

The destination endpoint contains its resolved immutable zone ID and this
exact traffic filter:

```json
{
  "type": "IP_ADDRESS",
  "ipAddressFilter": {
    "type": "IP_ADDRESSES",
    "matchOpposite": false,
    "items": [{"type": "IP_ADDRESS", "value": "198.51.100.20"}]
  },
  "portFilter": {
    "type": "PORTS",
    "matchOpposite": false,
    "items": [{"type": "PORT_NUMBER", "value": 1514}]
  }
}
```

The policy uses `IPV4`, the existing named `tcp` protocol representation with
`matchOpposite: false`, and `ALLOW` with `allowReturnTraffic: true`. It does not
add connection-state, IPsec, schedule, MAC, source-port, range, subnet, domain,
application, region, VPN, or traffic-matching-list fields.

## Normalized output

Add optional typed `source_filter` and `destination_filter` fields to the
normalized firewall policy. For IP-address traffic filters, preserve the outer
type, IP-filter type, `match_opposite`, every typed IP item, optional matching
list ID, optional port-filter type, port `match_opposite`, every typed port
item, and optional matching-list ID. This is read-only normalization and does
not add create support for subnets, ranges, or matching lists.

An absent upstream traffic filter omits the new normalized field, so existing
zone-only JSON remains compatible. The schema-v1 contract defines the optional
objects and their closed typed shapes.

## Apply and verification

Keep the existing high-impact experimental plan/apply flow. Planning resolves
zone selectors to immutable IDs. Apply immediately re-observes those IDs before
one POST. `--dry-run` wins over all apply flags.

After the POST, require a valid returned policy UUID and read exactly that ID.
Strictly normalize the observed supported security scope and require:

- enabled state, action, and return-traffic setting match;
- source and destination zone IDs match;
- IP version and protocol match;
- source and destination filters contain exactly one `IP_ADDRESS` item each;
- destination ports contain exactly one `PORT_NUMBER` item;
- every `matchOpposite` is false;
- no additional address, port, range, subnet, matching-list, MAC, or other
  security selector exists.

Then compare the complete canonical writable document with the request. Any
missing, changed, extra, malformed, or non-normalizable field is a conflict.
The CLI never retries the write and does not delete automatically. Successful
output retains the immutable ID for the existing delete command.

## Proof and delivery

Use only RFC 5737 documentation addresses and synthetic UUIDs. Add focused
domain, CLI, schema, and compatibility tests before implementation. Run the
repository smoke, race, coverage, vulnerability, and affected native release
checks. No live controller mutation is permitted; the feature remains without
sacrificial-controller live qualification.
