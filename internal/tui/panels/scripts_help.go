package panels

// scriptsHelpText is the body of the Scripts help section. Rendered through
// a read-only TextEditor with language=markdown so search + scroll work for
// free. Aimed at technical users who haven't read the source.
const scriptsHelpText = `# Writing scripts

You can attach JavaScript that runs before and after each request.
There are three places to put a script:

  - A profile script runs for every request you send with this
    profile selected.
  - A tag script runs for every request in a category (the tags
    shown in the endpoint list).
  - A request script runs only for one specific endpoint.

Pre-request scripts can change the outgoing request. Post-request
scripts can read or rewrite the response. If more than one script
applies to a request, they all run — most-general first for pre,
most-specific first for post.

## The feather object

Everything your script can touch lives under a single 'feather'
object. There are no other globals.

### feather.request

The outgoing request. Set fields in a pre-script to change what
feather sends.

    feather.request.method        // "GET"
    feather.request.path          // "/org/{org}/gvc/{gvc}/workload"
    feather.request.url           // full URL with substituted path
    feather.request.headers       // { "X-Trace-Id": "abc" }
    feather.request.queryParams   // { limit: "10" }
    feather.request.body          // string or null
    feather.request.pathParams    // { org: "acme" }

### feather.response

The response. Available in post-scripts. You can also rewrite it
before it's displayed.

    feather.response.status       // 200
    feather.response.statusText   // "200 OK"
    feather.response.headers      // { "Content-Type": ["application/json"] }
    feather.response.body         // string
    feather.response.duration     // ms

### feather.context

Read or write the same context variables shown in the Context panel.
Values persist across requests in the same feather session.

    feather.context.set("orgId", "abc");
    feather.context.get("orgId");
    feather.context.delete("orgId");

### feather.console

Anything you log shows up in the Console tab of the response panel.

    feather.console.log("info", 42);
    feather.console.warn("careful");
    feather.console.error("nope");

### feather.abort(reason)

Stops the script. In a pre-script the HTTP call is skipped; in a
post-script the error is recorded and the response is still shown.

    if (!feather.context.get("token")) feather.abort("no token");

### feather.fetch(url, options?)

Make a side HTTP call from inside a script — useful for looking
things up or notifying webhooks. It returns synchronously.

    var r = feather.fetch("https://hooks.example.com/log", {
        method:    "POST",
        headers:   { "Content-Type": "application/json" },
        body:      JSON.stringify({ id: feather.context.get("orgId") }),
        timeoutMs: 5000   // optional; default 10000, max 30000
    });
    if (r.status >= 400) feather.console.warn("webhook:", r.status);

Return shape:

    {
      status:     201,
      statusText: "201 Created",
      headers:    { "X-Foo": ["bar"] },   // values are arrays
      body:       "...",                  // string; not auto-parsed
      duration:   42                      // ms
    }

Transport errors throw. Non-2xx responses do not — check 'r.status'
yourself.

### Metadata

Read-only fields describing the current request.

    feather.profile.name          // "default"
    feather.tag                   // "Workload"
    feather.endpoint.method       // "GET"
    feather.endpoint.path         // "/org/{org}/gvc/{gvc}/workload"
    feather.phase                 // "pre" | "post"
    feather.scope                 // "profile" | "tag" | "operation"

## Examples

### Add a trace header to every request

Profile scope, pre-request:

    feather.request.headers["X-Trace-Id"] = "trace-" + Date.now();

### Save an ID from a response for later

Request scope, post-request:

    var body = JSON.parse(feather.response.body || "{}");
    if (body.id) feather.context.set("lastId", body.id);

### Block a request when something's missing

Tag scope, pre-request:

    if (!feather.context.get("orgId")) {
        feather.console.error("orgId missing");
        feather.abort("orgId not set");
    }

### Notify a webhook after every response

Profile scope, post-request:

    feather.fetch("https://hooks.example.com/log", {
        method:  "POST",
        headers: { "Content-Type": "application/json" },
        body:    JSON.stringify({
            op:       feather.endpoint.method + " " + feather.endpoint.path,
            status:   feather.response.status,
            duration: feather.response.duration
        })
    });

## Authentication

There is no built-in auth feature. Build the flow you need with a
pre-request script that injects whatever the API expects.

### Static bearer token

Pre-request script. Store the token in the Context panel (or set it
once with feather.context.set) so it survives across requests but
isn't checked into your overlay.

    var t = feather.context.get("apiToken");
    if (!t) feather.abort("apiToken missing — set it in Context");
    feather.request.headers["Authorization"] = "Bearer " + t;

### OAuth client-credentials, cached in context

Profile scope, pre-request. Refetches the token only when missing
or expired; subsequent requests reuse the cached one.

    var now    = Date.now();
    var token  = feather.context.get("accessToken");
    var expiry = parseInt(feather.context.get("accessTokenExp") || "0", 10);

    if (!token || now >= expiry) {
        var r = feather.fetch("https://auth.example.com/oauth/token", {
            method:  "POST",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body:    "grant_type=client_credentials" +
                     "&client_id="     + feather.context.get("clientId") +
                     "&client_secret=" + feather.context.get("clientSecret")
        });
        if (r.status >= 400) feather.abort("token fetch failed: " + r.status);
        var body = JSON.parse(r.body);
        token  = body.access_token;
        expiry = now + (body.expires_in * 1000) - 60000; // 1 min skew
        feather.context.set("accessToken",    token);
        feather.context.set("accessTokenExp", String(expiry));
    }
    feather.request.headers["Authorization"] = "Bearer " + token;

### Basic auth

There's no 'btoa' in scripts, so pre-encode 'user:password' once
(many shells: 'printf %s 'u:p' | base64') and store the result in
the Context panel:

    feather.request.headers["Authorization"] =
        "Basic " + feather.context.get("basicAuth");

## Things to know

- Scripts have up to 5 seconds to run before they're cut off.
- Promises exist but everything is synchronous — don't return a
  Promise from a script.
- There's no 'setTimeout' or 'setInterval'. Use the 'timeoutMs'
  option on 'feather.fetch' to bound a single call.
- 'feather.fetch' takes a full URL — it doesn't prepend the
  profile's base URL or apply anything you set on feather.request.
- Scripts can't access the filesystem.
- Context values live in memory for the session. Anything you want
  to persist across sessions belongs in the Context panel (which
  saves to the profile).
`
