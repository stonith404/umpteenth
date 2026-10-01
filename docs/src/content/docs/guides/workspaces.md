---
title: Workspaces
seoTitle: Workspaces, members and roles
description: Share Umpteenth with your team in workspaces with owners, admins and members, invite people by link or email, and switch between workspaces.
---

A workspace holds jobs, runs, secrets, providers, MCP servers and settings, and its members work with them according to their role.

## One workspace or several

With `workspaces.enabled` off, the default, everyone who signs in joins the same workspace, named **Default**.
The first person to sign in owns it, and everyone after joins as a member until an admin gives them another role.

To let people have several workspaces, turn the option on in `config.yml` and restart Umpteenth:

```yaml title="config.yml"
workspaces:
  enabled: true
```

The sidebar then shows the workspace switcher at its bottom, and admins rename a workspace in the **Workspace** card under **Settings → General**.
Someone who signs in without an invite gets a workspace of their own, named after them, such as **Ada Lovelace's workspace**, while an invite takes them straight to the workspace that invited them.
Every workspace starts without providers, so its admins add their own under [**Settings → Providers & models**](../models/).

Turning the option off again signs out everyone whose session is in another workspace, and their next sign-in lands in **Default**.
The other workspaces keep their data, and their schedules and webhooks keep running, so delete the ones you no longer need first.

## Roles

| Role | Can |
|---|---|
| **Member** | Create, change, run and delete jobs, cancel and retry runs, manage secrets and MCP servers, and create API tokens of their own. Members see the providers, the settings and the member list without changing them. |
| **Admin** | Everything a member can, and delete runs, change providers, models and **Settings → General**, invite and remove members, change their roles, and see and revoke every API token of the workspace. |
| **Owner** | Everything an admin can, and delete the workspace or hand it over to another member. A workspace has one owner. |

An API token acts with its creator's role and stops working once the creator loses access, as [REST API](../../reference/api/#api-tokens) explains.

## Invite people

With workspaces on, admins invite people under **Settings → Members** with **Invite member**.
The **Invite to workspace** dialog offers two kinds of invite:

- **By email**: enter an **Email** and click **Invite**.
  The person joins the next time they sign in with that address, even if they have an account already, so an invite never reveals who has one.
- **Invite link**: click **Create link**.
  Umpteenth shows the link once, and it works for whoever joins with it first.

For either kind, pick the **Role**, **Member** or **Admin**, and under **Expires after** how long the invite lasts: 1, 7 or 30 days.
Email invites match only an address that your [sign-in provider](../../deployment/sign-in/) verifies or an instance admin entered for a passkey account, so send a link to anyone else.

The link opens a page with the workspace's name, the role and who invited you, and **Join workspace** accepts it.
Someone who has never signed in before joins as part of their first sign-in, and someone without any account can [create a passkey account](../../deployment/sign-in/#add-people) from the link.

**Pending invites** on the same page lists the open invites, and **Revoke** in an invite's **⋯** menu withdraws one.

## Manage members

Admins change a member's role in the **Role** column of **Settings → Members**, and the change takes effect right away.
**Remove from workspace** in a member's **⋯** menu ends their access and stops the API tokens they created in the workspace.

The owner hands the workspace over with **Make owner** in the same menu.
That member becomes the owner and you become an admin.

With workspaces off, **Remove from workspace** is missing, since everyone joins again at their next sign-in.
Deactivate the user under [**Admin → Users**](#instance-admins) or at your sign-in provider instead.

## Switch, leave or delete workspaces

Click the workspace at the bottom of the sidebar to switch to another of your workspaces or to create one with **Create workspace**.

Admins and members leave a workspace with **Leave workspace** under **Settings → General → Danger zone**, and leaving your last one gives you a new workspace of your own.
The owner sees **Delete workspace** there instead, and hands the workspace over first to leave it.

To delete the workspace, the owner types its name in the confirmation, and Umpteenth removes every job, run, secret, provider and MCP server in it.
Umpteenth refuses to delete a workspace with active runs, so wait for them to finish or cancel them first.

## Instance admins

Instance admins see everyone who signed in and every workspace under **Admin** in the sidebar, and they have the owner's rights in every workspace.
You make someone an instance admin in the options of the provider they sign in with, or with **Make instance admin** for a passkey account, as [Sign-in](../../deployment/sign-in/#instance-admins) shows.

**Admin → Users** lists every user with how they sign in, their workspace count and their last sign-in, and **Add user** creates a [passkey account](../../deployment/sign-in/#add-people).
**Deactivate** in a user's **⋯** menu signs them out everywhere, stops their API tokens and refuses their sign-ins with "Your account has been deactivated, ask an admin to reactivate it".
Their memberships stay, so **Reactivate** in the same menu restores their access.

With workspaces on, **Admin → Workspaces** lists every workspace with its owner and member count.
**Open** switches you to a workspace without joining it, and **Delete** in its **⋯** menu deletes it once you type its name.
