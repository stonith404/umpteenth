## v0.1.0

### Features

- add internal passkey authentication ([5a2435b](https://github.com/stonith404/umpteenth/commit/5a2435b6b8c439f8029ab1784a9dae228397fb2e) by @stonith404)

### Bug Fixes

- only deploy the docs and publish next images from the main branch ([355c31b](https://github.com/stonith404/umpteenth/commit/355c31b4617920f008f7b717aa31ac5ad8a47263) by @stonith404)
- label free-form learning kinds safely and stop attributing combined diffs to one proposal ([457016f](https://github.com/stonith404/umpteenth/commit/457016f97a327298eb0d825e8bf4fb65562e7aed) by @stonith404)
- reject API token expiries too far ahead for browsers to show ([0e3dab3](https://github.com/stonith404/umpteenth/commit/0e3dab3dcd64347ead78433b09e5d5621db7df24) by @stonith404)
- load every provider page for the model pickers ([17106c2](https://github.com/stonith404/umpteenth/commit/17106c2b2d42c3ac58e94d0eeea460d922790cbb) by @stonith404)
- keep catalog-following models locked while the catalog loads ([82ad4be](https://github.com/stonith404/umpteenth/commit/82ad4be5e5357b11db5858328698dc6471db45d5) by @stonith404)
- only show a provider test result next to the model it tested ([026549d](https://github.com/stonith404/umpteenth/commit/026549dd0035abc11d91e6e27ae4d4d9737f1b32) by @stonith404)
- require an API key for providers on an official API ([ed644f4](https://github.com/stonith404/umpteenth/commit/ed644f4a6634a3c937c45687c5ccb80ff2cca40d) by @stonith404)
- stop state saves from overwriting keys changed meanwhile ([5917d52](https://github.com/stonith404/umpteenth/commit/5917d524afe507da38cccc3dcbd1d318cb25cf24) by @stonith404)
- keep dialogs open while their save is pending ([904c698](https://github.com/stonith404/umpteenth/commit/904c698d902780d2bb6bcade1a76e27d7a90a66f) by @stonith404)
- rate limit job compiles and stdio MCP server tests per person ([4edc1cd](https://github.com/stonith404/umpteenth/commit/4edc1cd9f44b8beab3e37d0336defbf54e8d8227) by @stonith404)
- deduplicate discovered OAuth scopes in linear time ([afdbecf](https://github.com/stonith404/umpteenth/commit/afdbecfb814c13634fd065bee5782eddbaf97189) by @stonith404)
- retry storing refreshed MCP OAuth tokens instead of dropping them ([b6759a3](https://github.com/stonith404/umpteenth/commit/b6759a307b1f33083c050d01d92c2a29ea75d92f) by @stonith404)
- bind MCP OAuth logins to the server URL and client they started for ([657d4b8](https://github.com/stonith404/umpteenth/commit/657d4b8b7b564f6941f340134fe8510b221af7da) by @stonith404)
- judge zoned IPv6 destinations by the address they reach in egress checks ([83a8275](https://github.com/stonith404/umpteenth/commit/83a827590f4aa6d7f4d4090b07ba5156584b5f09) by @stonith404)
- bypass the environment proxy in egress-guarded clients ([5595f9e](https://github.com/stonith404/umpteenth/commit/5595f9e51e122244278604191bede937b74d56d3) by @stonith404)
- treat webhook redirects as rejected deliveries instead of following them ([de41aab](https://github.com/stonith404/umpteenth/commit/de41aaba177f3d123953cc706d7a91f67f12ff92) by @stonith404)
- log only the start of failure reasons a sandbox reports ([914ddbf](https://github.com/stonith404/umpteenth/commit/914ddbfd435c989cdd87a99b98c200f359aa2680) by @stonith404)
- close both ends of a proxy tunnel once either direction ends ([643599d](https://github.com/stonith404/umpteenth/commit/643599d960e47e4a2f0e80e94dd3e96cfb5fc91a) by @stonith404)
- bound the broker requests a run may queue and refuse the rest with 429 ([98d2886](https://github.com/stonith404/umpteenth/commit/98d2886da2361f3480b8417fa4310c9390110360) by @stonith404)
- cap the hosts a proxy grant remembers at the timeline limit ([ec301fe](https://github.com/stonith404/umpteenth/commit/ec301fee43e226ddd722e71c74d48d31563183b4) by @stonith404)
- run the read-only ump copy for root execs on Kubernetes ([a3c7576](https://github.com/stonith404/umpteenth/commit/a3c7576ae64590cd331630535f6c57d85bdfef0d) by @stonith404)
- archive sandbox directories as the agent on Kubernetes ([e27fc12](https://github.com/stonith404/umpteenth/commit/e27fc12387667d68525e87e6c2275790065d4ade) by @stonith404)
- create exec shim pid files without following planted symlinks ([a4bca36](https://github.com/stonith404/umpteenth/commit/a4bca3670c8f52610f5eb0c7b0ec563aaabfbdd6) by @stonith404)
- probe the configured server host in the healthcheck ([ed6412a](https://github.com/stonith404/umpteenth/commit/ed6412a648e09a48cadeb3b73116853e017d072f) by @stonith404)
- honor Accept-Encoding quality values for precompressed assets ([9b1e51a](https://github.com/stonith404/umpteenth/commit/9b1e51add61b84f47c6255ed467a499c9cf5829a) by @stonith404)
- log and tag handler errors with the request ID ([04d1070](https://github.com/stonith404/umpteenth/commit/04d10708aa36d4e53ff5f0ccd711ed331e8636b3) by @stonith404)
- reject provider base URLs that carry credentials ([32ff7de](https://github.com/stonith404/umpteenth/commit/32ff7de94770da0cd86de02da54e13ee0198d158) by @stonith404)
- cap the tool calls of one model answer ([12b6b41](https://github.com/stonith404/umpteenth/commit/12b6b417de53426a91a4353bed5d544e01b4ff8c) by @stonith404)
- batch and bound live tool output events ([f171706](https://github.com/stonith404/umpteenth/commit/f1717067a689d8d2cf9a468db6d81eea26104620) by @stonith404)
- arm a job's new schedule alarm before dropping the pending one ([5e2bd15](https://github.com/stonith404/umpteenth/commit/5e2bd150a909e1753d49a341246f63f879f9612f) by @stonith404)
- queue runs the job actor lost track of under the queue policy ([11a6f4f](https://github.com/stonith404/umpteenth/commit/11a6f4f2ff7537a04db06390e8c9df2576f4eb99) by @stonith404)
- retry a failed run submission with a job actor alarm ([a392b76](https://github.com/stonith404/umpteenth/commit/a392b76d09d09dbe9752025bf553809b8d0dda5f) by @stonith404)
- decline run tasks on replicas without a sandbox adapter so they reroute ([d7f309b](https://github.com/stonith404/umpteenth/commit/d7f309b30b6620b087d6344bedb130f288d98984) by @stonith404)
- record a reflection's spend and result together and retry a failed write ([3f8dc84](https://github.com/stonith404/umpteenth/commit/3f8dc843d8be6a7419a48c09ded3538852624bd0) by @stonith404)
- create an MCP tool's schema editor only once its row opens ([7c03153](https://github.com/stonith404/umpteenth/commit/7c03153384262fe3428bff632b05ef29befd5cca) by @stonith404)
- cap the lines a diff view renders ([7a85ae6](https://github.com/stonith404/umpteenth/commit/7a85ae6e3a9befc522e719eeeb6775d08fd1ae93) by @stonith404)
- keep new-job drafts per user and workspace and drop them on sign-out ([740cf3a](https://github.com/stonith404/umpteenth/commit/740cf3aa9172d118fde83fdc05914961c50e9472) by @stonith404)
- follow background reflection changes on the environment tab ([353042a](https://github.com/stonith404/umpteenth/commit/353042a4f157ef79beebd122da76caa99b8082f6) by @stonith404)
- stop job settings cards from overwriting changes saved elsewhere ([521c6f5](https://github.com/stonith404/umpteenth/commit/521c6f501bba8908f68da2b4240f3be62952fd7a) by @stonith404)
- let only one person join through an invite link accepted concurrently ([cde672a](https://github.com/stonith404/umpteenth/commit/cde672af00da33c11733eb8e756d63714465105a) by @stonith404)
- keep a workspace owner when a handover races a role change or removal ([f6077d7](https://github.com/stonith404/umpteenth/commit/f6077d775be8c9494349db63d74c1a7771dbed6a) by @stonith404)
- keep email invites pending so they don't reveal which addresses have an account ([6b909f7](https://github.com/stonith404/umpteenth/commit/6b909f72299ee976985565b687146640b4026363) by @stonith404)
- end the session on the server when signing out or deactivating a user ([104bbcc](https://github.com/stonith404/umpteenth/commit/104bbccc0aa33c692f8a3a3ff658ddfcf8f6b623) by @stonith404)
- tie listed GitHub names to their holders on start instead of at first sign-in ([90b8d7e](https://github.com/stonith404/umpteenth/commit/90b8d7efa2ed72757af11bbe210ff307de96249f) by @stonith404)
- fail a broker connect whose endpoint the engine does not report ([85c8c0e](https://github.com/stonith404/umpteenth/commit/85c8c0ed238eb242bc289c25e93c58c2e4fd022a) by @stonith404)
- route image builds only to replicas that have a builder ([3cdf279](https://github.com/stonith404/umpteenth/commit/3cdf27924ea55516c50803500b5c7a6584e524e2) by @stonith404)
- refuse every private address in the egress proxy while Kubernetes cluster ranges are unset ([a7cd9b0](https://github.com/stonith404/umpteenth/commit/a7cd9b00cd3926ae5e2d495ebe42133ead2ff2d9) by @stonith404)
- reuse unfinished image builds and cap them per job ([80479a6](https://github.com/stonith404/umpteenth/commit/80479a67c5c8850d015380e67a138ee48814dd49) by @stonith404)
- keep job image builds from fetching sources past the egress proxy ([fed93b0](https://github.com/stonith404/umpteenth/commit/fed93b09b6d16a0db240b6733c95992bb8506a50) by @stonith404)
- count only the work a stopped demo run did before the stop ([6ea2368](https://github.com/stonith404/umpteenth/commit/6ea23687dd524434b8737fc8db419f4fe0f09b05) by @stonith404)
- improve text width on login page and empty states ([dd85d5e](https://github.com/stonith404/umpteenth/commit/dd85d5e53603421f0358bc9d35ed5e3802093c42) by @stonith404)

### Documentation

- simplify example config ([1ca3730](https://github.com/stonith404/umpteenth/commit/1ca3730ae3a19bac4ef85f8a9458fda8f1ff6b98) by @stonith404)

### Other

- add clusterRanges to test setup ([228505a](https://github.com/stonith404/umpteenth/commit/228505a57d82f46af7690ce671e87309c512b639) by @stonith404)
- adapt config.yml validation tests ([7e0499c](https://github.com/stonith404/umpteenth/commit/7e0499c24864b19d97f03ae9b9c2f090655a6e28) by @stonith404)

