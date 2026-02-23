# Extractly Pack (ACL)

This pack lets you call Extractly's API from ACL **without writing a Go tool wrapper**.

It uses the built-in `http.request` tool and a simple tool manifest.

## What this proves

- Existing API-first apps can be used by ACL today
- You do not need to write Go to get started
- ACL can add checks + receipts on top of a normal REST API

## Setup

1. Install the pack:

```bash
acl pack install extractly
cd acl-packs/extractly
```

2. Copy sample vars and add your key:

```bash
cp samples/vars.map.json vars.json
# edit vars.json and set api_key
```

3. Run a template:

```bash
acl run templates/map_urls.acl --vars vars.json
```

## Why `request_json` is used for POST templates

ACL v0.1 does not yet have first-class object literals for step arguments, so this pack accepts the HTTP request body as a JSON string (`request_json`) and passes it directly to `http.request`.

This still gives you:
- ACL checks
- receipts
- reusable workflows

Later, ACL tool manifests + adapter generation can make this even smoother.

## Included templates

- `templates/templates_list.acl` -> GET `/api/templates`
- `templates/map_urls.acl` -> POST `/api/map`
- `templates/scrape_site.acl` -> POST `/api/scrape`
- `templates/extract_data.acl` -> POST `/api/extract`

## Sample payload vars

- `samples/vars.map.json`
- `samples/vars.scrape.json`
- `samples/vars.extract.json`

