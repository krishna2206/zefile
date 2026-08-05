# Changelog

## [0.8.0](https://github.com/krishna2206/zefile/compare/v0.7.0...v0.8.0) (2026-08-05)


### Features

* extract ZIP archives as a background job ([7db77d2](https://github.com/krishna2206/zefile/commit/7db77d2a75fb34df35c4cee73b6b386a055d2adb))

## [0.7.0](https://github.com/krishna2206/zefile/compare/v0.6.0...v0.7.0) (2026-08-04)


### Features

* back up and restore the database from the CLI ([d4a9797](https://github.com/krishna2206/zefile/commit/d4a979768cd3240e50a305e68a3ef44a35961a7a))
* reset a forgotten password with recovery codes ([8fb663d](https://github.com/krishna2206/zefile/commit/8fb663deb9c4206555d1c34a4d5d6bc558c5b00d))
* retention, checksums, and a uniform settings UI ([42d3ae0](https://github.com/krishna2206/zefile/commit/42d3ae03127d7c6c40956884516f64390c9ebe6c))
* show a place, not an IP, for each session ([f293f7c](https://github.com/krishna2206/zefile/commit/f293f7c2974b2551d3a50ac068448a30db76b718))
* show the logo in the sign-in, sidebar and breadcrumb ([d61d62d](https://github.com/krishna2206/zefile/commit/d61d62dbce3ea303a8cb592143377e6d68bce91d))
* snapshot before migrations and report divergences on restore ([e1afd62](https://github.com/krishna2206/zefile/commit/e1afd62a3538f72927e63b185d8852bab45263dc))
* two-factor authentication (TOTP) ([eaa9f88](https://github.com/krishna2206/zefile/commit/eaa9f8861292dbfc7122d4174d3563f6bd0aa36b))


### Bug Fixes

* record the real client IP behind a reverse proxy ([0822320](https://github.com/krishna2206/zefile/commit/0822320576922eda75b156507d6d2124cd5b98aa))

## [0.6.0](https://github.com/krishna2206/zefile/compare/v0.5.0...v0.6.0) (2026-08-02)


### Features

* account menu in the sidebar footer ([73f693b](https://github.com/krishna2206/zefile/commit/73f693b31fa36c1f067606c22484be05753f45d8))
* account settings — change password and manage sessions ([5d40ab9](https://github.com/krishna2206/zefile/commit/5d40ab96f4ee2dd9bba07f23b7914ead71306a50))
* API tokens for programmatic access ([892ebdd](https://github.com/krishna2206/zefile/commit/892ebdd5eb3fffa51c0cde4b7e6a4746de573048))
* audit log of who did what ([242d5a3](https://github.com/krishna2206/zefile/commit/242d5a3bb7784d671afc6f4f3493a807b7fbd1ca))
* download a selection or a folder as a zip ([63f4ca5](https://github.com/krishna2206/zefile/commit/63f4ca57fffc8a5249b5a7b8a9314a6ede76de00))
* preview video, audio and text as well as images and PDF ([a04b034](https://github.com/krishna2206/zefile/commit/a04b034431d02acb220f784eca220b8eeebd369e))
* user groups for granting access to a team at once ([2ec1e81](https://github.com/krishna2206/zefile/commit/2ec1e812545ad77893a5d02d9cf4fc146be64c86))


### Bug Fixes

* Download saves the file instead of opening it inline ([0e0a2d6](https://github.com/krishna2206/zefile/commit/0e0a2d6fa03f35a63c2a9f2b3dbdedb3d4972190))
* stamp the version from version.txt when none is passed to the build ([119558c](https://github.com/krishna2206/zefile/commit/119558ccbbeca3dbc8ff0f639f2e247ba855c22b))
* the context-menu Download actually downloads ([41cd63a](https://github.com/krishna2206/zefile/commit/41cd63a74efade6be019361eddd3f927302f21b0))

## [0.5.0](https://github.com/krishna2206/zefile/compare/v0.4.0...v0.5.0) (2026-07-31)


### Features

* copy and move in the selection action bar ([eebbeb5](https://github.com/krishna2206/zefile/commit/eebbeb54623f1a226d2ec04095c5d274428cd778))
* edit permissions inline in the manage-access dialog ([f16dc97](https://github.com/krishna2206/zefile/commit/f16dc97ba985e7319d2617341892a16399adb35f))
* grant access and enforce the share permission ([aec2218](https://github.com/krishna2206/zefile/commit/aec221813634131a6c73f6efb9e10286f6464bf2))
* invite people to create accounts ([e2e7243](https://github.com/krishna2206/zefile/commit/e2e7243d8c57fa3ed3411df5f7007b93d9840010))
* manage existing accounts from the Members screen ([c1a6c17](https://github.com/krishna2206/zefile/commit/c1a6c17513ad02b47c86c186da8ced86334c377b))
* show only the actions the caller is allowed to perform ([abef97e](https://github.com/krishna2206/zefile/commit/abef97e31cc81785c54b78c999db7c48253d4180))


### Bug Fixes

* let a granted user reach files through folders above them ([be04664](https://github.com/krishna2206/zefile/commit/be04664979afd6f90e60a0ba6d7119fcffb5d322))
* pasting clears the clipboard so Paste no longer lingers ([22cf970](https://github.com/krishna2206/zefile/commit/22cf9706c60c8f1f24f79da7c0fe64582e338f73))
* renaming a folder carries its permissions and ownership ([8b5a599](https://github.com/krishna2206/zefile/commit/8b5a599fe6cba9889c1f3f436534c8f2bdc2de1b))

## [0.4.0](https://github.com/krishna2206/zefile/compare/v0.3.0...v0.4.0) (2026-07-31)


### Features

* background job queue, copying folders and large files ([f40133a](https://github.com/krishna2206/zefile/commit/f40133a0029a3a63d5e32b70f04b17785ab083b9))
* copy, cut and paste files and folders ([a2b33a3](https://github.com/krishna2206/zefile/commit/a2b33a36cbae9e2b5153e6713be135be79e9af8d))
* drag a file onto a folder to move it ([6cbf180](https://github.com/krishna2206/zefile/commit/6cbf180bab6f1e33ccef4ff959ebabe6c312ed2d))
* drop a folder onto the page to upload its whole tree ([2ab708c](https://github.com/krishna2206/zefile/commit/2ab708c4d83f12ef4321eb1100aa228bbc3cc7f9))
* make Share the only external link, expiring by default ([015fa27](https://github.com/krishna2206/zefile/commit/015fa274105c068fa7c6964a4946e04c9d333e3c))
* mark shared files with a link badge in the grid ([39d5313](https://github.com/krishna2206/zefile/commit/39d5313afbc73418131019ed1b5962fbc00b9e56))
* password option in the share dialog ([986c3cd](https://github.com/krishna2206/zefile/commit/986c3cd7dc68eef839da9a94167a66544a36bdd2))
* password-protected share links ([ff484b8](https://github.com/krishna2206/zefile/commit/ff484b8e1f48286321e78ba27efb7b84ad6441ed))
* public folder shares with confined browsing ([57ab3a9](https://github.com/krishna2206/zefile/commit/57ab3a9361f8b92c551ba0dd3395979273625071))
* public share links for files ([e705232](https://github.com/krishna2206/zefile/commit/e705232f1c9003e9c46e8298c7fa7d280f9787d8))
* recursive file search from the toolbar ([93b0007](https://github.com/krishna2206/zefile/commit/93b000717a05c3fb76b7415adea2d9537252f306))
* share dialog and a Shared section ([2a3d04b](https://github.com/krishna2206/zefile/commit/2a3d04b85701966b781e62a091ccbe6bdbccdae5))
* share folders from the context menu ([e4e6e81](https://github.com/krishna2206/zefile/commit/e4e6e81955aa33562d4f61b4192141546792337a))
* show the share badge in list view too ([1a174d6](https://github.com/krishna2206/zefile/commit/1a174d6a3cfb574ac968d65ab1e4a569a8cfd6e9))
* unify creating with a New menu and empty-area right-click ([c06d396](https://github.com/krishna2206/zefile/commit/c06d39669f5e30e27c307ed09bb497c6444c5ec5))

## [0.3.0](https://github.com/krishna2206/zefile/compare/v0.2.0...v0.3.0) (2026-07-30)


### Features

* load thumbnails from the endpoint, show the upload queue ([54e419b](https://github.com/krishna2206/zefile/commit/54e419bcb5bd6241b1f360aee9366d787a7afa47))
* serve compressed image thumbnails from the server ([de52537](https://github.com/krishna2206/zefile/commit/de525374cb7ea99afed6471c477592b933396a89))


### Bug Fixes

* thumbnails never loaded because the image was display:none ([74d6ea8](https://github.com/krishna2206/zefile/commit/74d6ea8fd1d1249ef24214b4f5866281b4a4585d))
