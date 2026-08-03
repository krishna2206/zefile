# Previews

Files can be previewed in place, in a full-screen overlay, with keyboard
navigation between files in the same folder.

## What can be previewed

- **Images** and **PDF**
- **Video**, with seeking, and **audio**
- **Text** — including code and Markdown, shown **as source**, without syntax
  highlighting or rendering

## A deliberate decision: the server never parses documents

Everything relies on what the browser renders natively — images, video, audio,
PDF in a sandboxed iframe — while text is shown as raw source. There is **no
document parser in the backend**, and therefore no library-parsing
vulnerability to track. That is precisely the category of problems the author of
File Browser gave up trying to fix.

## Requires two origins

In-place preview needs a separate content origin. In single-origin mode every
file is sent as a download instead. See
[One origin, or two?](/guide/installation#one-origin-or-two).
