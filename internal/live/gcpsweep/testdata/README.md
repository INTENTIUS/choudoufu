# Cloud Asset Inventory: the request the sweep sends and the shapes its fakes return

This is the specification `gcpsweep_test.go`'s fakes serve, and the one the
emulator's Asset Inventory surface (lex00/floci-gcp#7) is to be checked
against. The bodies in the test file are these, byte for byte where shown.
Where a shape has not been seen from the real API, it says so.

## The request

One request per page, per project:

```
GET {root}v1/projects/{PROJECT}:searchAllResources
    ?alt=json
    &prettyPrint=false
    &query=labels.tofu-estate%3D{ESTATE}
    &pageSize=500
    &readMask=name%2CassetType%2Cproject%2Clocation%2Clabels%2CcreateTime%2CupdateTime
    [&pageToken={nextPageToken of the previous page}]
Authorization: Bearer {access token}
[X-Goog-User-Project: {billing_project, else PROJECT}]   only with user_project_override
```

- `{root}` is `https://cloudasset.googleapis.com/`, or the provider's
  `cloud_asset_custom_endpoint` with a trailing `v1/` removed, so
  `http://localhost:4566/cloudasset/v1/` sends to
  `http://localhost:4566/cloudasset/v1/projects/p:searchAllResources`.
- `{PROJECT}` is the project id the provider block names (the API also
  accepts a project number).
- No `assetTypes` and no `orderBy`: every asset type, in the service's order.
- `pageToken` is absent on the first page, and the previous response's
  `nextPageToken` on each later one. The sweep stops at the first response
  with no `nextPageToken`, and fails (as partial) if a response repeats the
  token it was sent.
- `alt=json` and `prettyPrint=false` are added by the generated Go client.

Source: https://cloud.google.com/asset-inventory/docs/reference/rest/v1/TopLevel/searchAllResources
(path, parameters, `pageSize` maximum of 500, `readMask`), and the query
syntax at https://cloud.google.com/asset-inventory/docs/searching-resources#how_to_construct_a_query.

## Responses

### A page with more to come (200)

```json
{
  "results": [
    {
      "name": "//compute.googleapis.com/projects/p1/global/networks/net-a",
      "assetType": "compute.googleapis.com/Network",
      "project": "projects/111111111111",
      "location": "global",
      "labels": {"tofu-estate": "est", "tofu-address": "google_compute_network.a"},
      "createTime": "2026-10-01T10:00:00Z",
      "updateTime": "2026-10-02T11:00:00Z"
    },
    {
      "name": "//storage.googleapis.com/bucket-b",
      "assetType": "storage.googleapis.com/Bucket",
      "project": "projects/111111111111",
      "location": "us-central1",
      "labels": {"tofu-estate": "est"}
    }
  ],
  "nextPageToken": "page-2"
}
```

`project` is `projects/` and the project NUMBER, not the id searched by.
`createTime` and `updateTime` are RFC 3339 and absent for an asset type that
reports none. Fields outside the read mask are not returned.

Source: `SearchAllResourcesResponse` and `ResourceSearchResult` at
https://cloud.google.com/asset-inventory/docs/reference/rest/v1/ResourceSearchResult.

### The last page (200)

The same shape with no `nextPageToken`. The fake's second page also carries
a resource labelled `tofu-estate: est-east`, which the client drops: the
query asks for an exact match, and the client checks it again.

### Nothing found (200)

```json
{}
```

No `results` and no `nextPageToken`. This is the only empty answer the sweep
accepts.

### API not enabled on the project (403)

```json
{
  "error": {
    "code": 403,
    "message": "Cloud Asset API has not been used in project 111111111111 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/cloudasset.googleapis.com/overview?project=111111111111 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry.",
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "SERVICE_DISABLED",
        "domain": "googleapis.com",
        "metadata": {
          "service": "cloudasset.googleapis.com",
          "consumer": "projects/111111111111",
          "activationUrl": "https://console.developers.google.com/apis/api/cloudasset.googleapis.com/overview?project=111111111111",
          "serviceTitle": "Cloud Asset API",
          "containerInfo": "111111111111"
        }
      }
    ]
  }
}
```

The real error also carries `google.rpc.LocalizedMessage` and
`google.rpc.Help` details; the client reads only `ErrorInfo`, and falls back
to the message text and to the legacy `errors[].reason` of
`accessNotConfigured` when there is none.

Source: https://cloud.google.com/apis/design/errors#error_info (ErrorInfo,
SERVICE_DISABLED), https://cloud.google.com/service-usage/docs/troubleshooting
(the message).

### Permission denied, bare (403)

```json
{
  "error": {
    "code": 403,
    "message": "The caller does not have permission",
    "status": "PERMISSION_DENIED"
  }
}
```

Reported as missing `cloudasset.assets.searchAllResources`, the one
permission the call needs.

Source: https://cloud.google.com/apis/design/errors#handling_errors
(PERMISSION_DENIED), https://cloud.google.com/asset-inventory/docs/access-control
(the permission).

### Permission denied, naming the permission (403)

```json
{
  "error": {
    "code": 403,
    "message": "Permission 'cloudasset.assets.searchAllResources' denied on resource 'projects/p1' (or it may not exist).",
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "IAM_PERMISSION_DENIED",
        "domain": "cloudasset.googleapis.com",
        "metadata": {
          "permission": "cloudasset.assets.searchAllResources",
          "resource": "projects/p1"
        }
      }
    ]
  }
}
```

Not seen from the Asset API itself: this is the IAM_PERMISSION_DENIED shape
newer Google APIs return. The client takes `metadata.permission` when present.

Source: https://cloud.google.com/apis/design/errors#error_info.

### A later page fails (400 on page two)

```json
{
  "error": {
    "code": 400,
    "message": "Invalid page token.",
    "status": "INVALID_ARGUMENT"
  }
}
```

Served after a good first page. The client returns no results and a
`PartialError`; the 403 bodies above are also served on page two, and the
typed error is still reachable through it.

Source: https://cloud.google.com/apis/design/errors#handling_errors
(INVALID_ARGUMENT). The exact message is not taken from the real API.

## Credentials (fakes in the same test file)

- OAuth token endpoint, `POST {token_uri}`: a service account key's JWT-bearer
  grant (`grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`,
  `assertion=...`), answered with
  `{"access_token":"...","token_type":"Bearer","expires_in":3600}`.
  Source: https://developers.google.com/identity/protocols/oauth2/service-account#httprest.
- IAM Credentials, `POST {root}v1/projects/-/serviceAccounts/{email}:generateAccessToken`
  with body `{"scope":[...],"lifetime":"3600s","delegates":["projects/-/serviceAccounts/{email}"]}`,
  answered with `{"accessToken":"...","expireTime":"2099-01-01T00:00:00Z"}`.
  `{root}` is `iam_credentials_custom_endpoint` with `v1/` removed.
  Source: https://cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateAccessToken.
