# Host ABI

The imports an Aidoku source (`main.wasm`, built with [aidoku-rs](https://github.com/Aidoku/aidoku-rs))
expects from its runner, and what kinkoku implements. Behavior follows
[AidokuRunner](https://github.com/Aidoku/AidokuRunner); the Swift file column points at the reference.

Conventions:

- Pointers and lengths are `i32` (wasm32). Host objects are passed as **descriptors** (`rid`, `i32`,
  starting at 1). `std.destroy` frees one; when the table is empty, numbering restarts at 1.
- Negative return values are error codes (listed per module below).
- Structured values are [postcard](https://postcard.jamesmunns.com/wire-format)-encoded. Arguments
  the host passes to exports are descriptors to postcard bytes (the search query is a raw UTF-8
  string). Buffers the guest hands to the host (export results, `defaults.set`, `canvas.fill` paths)
  are `[len u32][cap u32][payload]`, where `len` includes the 8-byte header; an error result is
  `[-1][cap][len][utf8 message]`.
- Any import kinkoku doesn't provide is linked to a stub that logs `unimplemented: module.name` and
  returns -1, so the source fails loudly instead of failing to load.

There is no `json` module in the current ABI: aidoku-rs parses JSON inside the wasm (serde_json).
`json` only existed in the pre-0.7 ABI, which the Aidoku Community repo no longer ships.

Status: done, partial, stub (logs `unimplemented`, returns -1), or unsupported (by design).

## env: `Imports/Env.swift` -> `abi/std.go`

| name | signature | status | notes |
|---|---|---|---|
| abort | `() -> ()` | done | no-op, as in AidokuRunner |
| print | `(ptr, len) -> ()` | done | to stderr, prefixed `[source id]` |
| sleep | `(seconds i32) -> ()` | done | |
| send_partial_result | `(ptr) -> ()` | done | ignored: the final result carries the same data |

## std: `Imports/Std.swift` -> `abi/std.go`, `abi/date.go`

Errors: -1 invalid descriptor, -2 invalid buffer size, -3 failed memory write, -4 invalid string, -5 invalid date string.

| name | signature | status | notes |
|---|---|---|---|
| destroy | `(rid) -> ()` | done | |
| buffer_len | `(rid) -> i32` | done | bytes or string descriptors |
| read_buffer | `(rid, buf, size) -> i32` | done | writes the first `size` bytes |
| current_date | `() -> f64` | done | unix seconds |
| utc_offset | `() -> i64` | done | seconds, sign as in Swift (`-secondsFromGMT`) |
| parse_date | `(str, len, fmt, len, locale, len, tz, len) -> f64` | partial | ICU patterns (y M L d H k h K m s S a E Z X x z O, quotes). Month names: en, es, it, fr, pt, de, id; other locales parse numeric dates only |

## net: `Imports/Net.swift` -> `abi/net.go`

Errors: -1 invalid descriptor, -2 invalid string, -3 invalid method, -4 invalid URL, -5 invalid HTML,
-6 invalid buffer size, -7 missing data, -8 missing response, -9 missing URL, -10 request error,
-11 failed memory write, -12 not an image.

| name | signature | status | notes |
|---|---|---|---|
| init | `(method i32) -> rid` | done | GET POST PUT HEAD DELETE PATCH OPTIONS CONNECT TRACE |
| send | `(rid) -> i32` | done | honors the rate limit; cookies from `cookies.json`; FlareSolverr retry on 403/503 challenges |
| send_all | `(rids ptr, count) -> i32` | done | concurrent; writes a per-request code array back over `rids` |
| set_url | `(rid, ptr, len) -> i32` | done | |
| set_header | `(rid, key, len, value, len) -> i32` | done | |
| set_body | `(rid, ptr, len) -> i32` | done | |
| set_timeout | `(rid, seconds f64) -> i32` | done | |
| data_len | `(rid) -> i32` | done | |
| read_data | `(rid, buf, size) -> i32` | done | |
| get_image | `(rid) -> rid` | done | jpeg, png, gif, webp |
| get_status_code | `(rid) -> i32` | done | |
| get_url | `(rid) -> rid` | done | final URL after redirects |
| get_header | `(rid, key, len) -> rid` | done | case-insensitive |
| html | `(rid) -> rid` | done | base URI = final URL |
| set_rate_limit | `(permits, period, unit) -> ()` | done | unit 0 s, 1 min, 2 h |

## html: `Imports/Html.swift` -> `abi/html.go`

Parsing is golang.org/x/net/html; selectors are andybalholm/cascadia with jsoup extensions rewritten:
`:eq(n)`, `:lt(n)`, `:gt(n)` and unquoted attribute values (`[href^=/a?b]`). Text follows
jsoup/SwiftSoup (whitespace normalized, block elements and `<br>` separate words). `html()`/`outer_html`
are not pretty-printed.

Errors: -1 invalid descriptor, -2 invalid string, -3 invalid HTML, -4 invalid query, -5 no result, -6 parse error.
Kinds (`kind`): 0 unknown, 1 node, 2 text, 3 data (script/style text), 4 comment, 5 element, 6 element list, 7 document.

| name | signature | status | notes |
|---|---|---|---|
| parse | `(html, len, base, len) -> rid` | done | `<base href>` updates the base URI |
| parse_fragment | `(html, len, base, len) -> rid` | done | everything lands in `<body>` |
| escape | `(ptr, len) -> rid` | done | |
| unescape | `(ptr, len) -> rid` | done | |
| kind | `(rid) -> i32` | done | |
| child_nodes | `(rid) -> rid` | done | node list (all node types) |
| has_attr | `(rid, key, len) -> i32` | done | supports `abs:` |
| set_attr | `(rid, key, len, value, len) -> i32` | done | empty value removes |
| remove_attr | `(rid, key, len) -> i32` | done | |
| set_text | `(rid, ptr, len) -> i32` | done | |
| set_html | `(rid, ptr, len) -> i32` | done | |
| prepend | `(rid, ptr, len) -> i32` | done | HTML |
| append | `(rid, ptr, len) -> i32` | done | HTML |
| children | `(rid) -> rid` | done | |
| base_uri | `(rid) -> rid` | done | |
| own_text | `(rid) -> rid` | done | |
| data | `(rid) -> rid` | done | element: script/style/comment data; data/comment node: its data |
| id | `(rid) -> rid` | done | |
| tag_name | `(rid) -> rid` | done | |
| class_name | `(rid) -> rid` | done | |
| has_class | `(rid, ptr, len) -> i32` | done | case-insensitive |
| add_class | `(rid, ptr, len) -> i32` | done | |
| remove_class | `(rid, ptr, len) -> i32` | done | |
| first | `(rid) -> rid` | done | |
| last | `(rid) -> rid` | done | |
| get | `(rid, index) -> rid` | done | element or node lists |
| size | `(rid) -> i32` | done | element or node lists |
| parent | `(rid) -> rid` | done | |
| siblings | `(rid) -> rid` | done | elements for an element, nodes otherwise |
| next | `(rid) -> rid` | done | next element sibling for elements |
| previous | `(rid) -> rid` | done | |
| attr | `(rid, key, len) -> rid` | done | `abs:` prefix; missing -> `""` |
| outer_html | `(rid) -> rid` | done | |
| remove | `(rid) -> i32` | done | |
| select | `(rid, query, len) -> rid` | done | includes the root element, like jsoup |
| select_first | `(rid, query, len) -> rid` | done | |
| text | `(rid) -> rid` | done | |
| untrimmed_text | `(rid) -> rid` | done | |
| html | `(rid) -> rid` | done | inner HTML |

## defaults: `Imports/Defaults.swift` -> `abi/defaults.go`

Stored per source in `<data>/sources/<id>/defaults.json`. Defaults from `settings.json` (plus the
implicit `languages`/`language` and `url` settings) are registered, not written.

Errors: -1 invalid key, -2 invalid value (missing key), -3 failed encoding, -4 failed decoding.
Value kinds: 0 data, 1 bool, 2 int (i32), 3 float (f32), 4 string, 5 string array, 6 null (delete).

| name | signature | status | notes |
|---|---|---|---|
| get | `(key, len) -> rid` | done | postcard-encoded value; data is returned raw |
| set | `(key, len, kind, value ptr) -> i32` | done | value is a `[len][cap][payload]` buffer |

## canvas: `Imports/Canvas.swift` -> `abi/canvas.go`

Enough for page descrambling. Images are decoded Go images; contexts are RGBA with an affine transform
(translate -> scale -> rotate, as CoreGraphics composes them).

Errors: -1 invalid context, -2 invalid image pointer, -3 invalid image, -4 invalid source rect,
-5 invalid result, -6 invalid bounds, -7 invalid path, -8 invalid style, -9 invalid string,
-10 invalid font, -11 invalid data, -12 font load failed.

| name | signature | status | notes |
|---|---|---|---|
| new_context | `(w f32, h f32) -> rid` | done | |
| set_transform | `(rid, tx, ty, sx, sy, rotate f32) -> i32` | done | |
| draw_image | `(rid, img, x, y, w, h f32) -> i32` | done | bilinear |
| copy_image | `(rid, img, sx, sy, sw, sh, dx, dy, dw, dh f32) -> i32` | done | |
| get_image | `(rid) -> rid` | done | snapshot of the context |
| new_image | `(ptr, len) -> rid` | done | |
| get_image_data | `(img) -> rid` | done | PNG bytes (raw bytes are copied) |
| get_image_width | `(img) -> f32` | done | |
| get_image_height | `(img) -> f32` | done | |
| fill | `(rid, path ptr, r, g, b, a f32) -> i32` | stub | no vector drawing yet |
| stroke | `(rid, path ptr, style ptr) -> i32` | stub | |
| draw_text | `(rid, text, len, size, x, y, font, r, g, b, a) -> i32` | stub | no fonts |
| new_font | `(name, len) -> rid` | stub | |
| system_font | `(weight) -> rid` | stub | |
| load_font | `(url, len) -> rid` | stub | |

## js: `Imports/JavaScript.swift`

| name | status | notes |
|---|---|---|
| context_create, context_eval, context_eval_async, context_get | unsupported | |
| webview_create, webview_set_rule_list, webview_load, webview_load_html, webview_wait_for_load, webview_eval, webview_eval_async, webview_add_user_script, webview_get_cookies, … | unsupported | |

Sources that import anything from `js` are marked `"supported": false` by `kinkoku list` and refuse to run.

## Exports kinkoku calls: `Interpreter.swift` -> `source/source.go`

| export | args | result | used by |
|---|---|---|---|
| start | – | – | every run |
| get_search_manga_list | query (raw string rid), page i32, filters rid (`Vec<FilterValue>`) | `MangaPageResult` | `search` |
| get_manga_list | listing rid, page i32 | `MangaPageResult` | `listing` |
| get_listings | – | `Vec<Listing>` | `listing` (dynamic listings) |
| get_manga_update | manga rid, needs_details i32, needs_chapters i32 | `Manga` | `manga`, `download` |
| get_page_list | manga rid (no chapters), chapter rid | `Vec<Page>` | `download` |
| get_image_request | url rid, context rid or -1 | net request rid (i32) | `download` |
| process_page_image | `ImageResponse` rid, context rid or -1 | image rid (i32) | `download` |
| free_result | result ptr | – | after every call |

Not called (out of scope): `get_home`, `get_filters`, `get_settings`, `process_cover_image`,
`get_page_description`, `get_alternate_covers`, `get_base_url`, `handle_notification`,
`handle_deep_link`, `handle_basic_login`, `handle_web_login`, `handle_key_migration`.
