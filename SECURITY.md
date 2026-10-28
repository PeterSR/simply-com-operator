# Security policy

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub's private vulnerability reporting: the
**Security** tab of [PeterSR/simply-com-operator](https://github.com/PeterSR/simply-com-operator),
then *Report a vulnerability*. If that form is unavailable, use the contact options on the
maintainer's [GitHub profile](https://github.com/PeterSR) to arrange a private channel. Do not open
a public issue for anything you believe is a security problem.

You can expect an acknowledgement within a few days.

## Scope

In scope:

- the operator changing or deleting a record at Simply.com that it did not create or adopt;
- a `DNSRecord` or `Account` in one namespace using credentials or reaching zones it should not;
- the API key leaking, for example into logs, events or status;
- the chart granting more than the operator needs.

Not vulnerabilities, by design:

- anyone allowed to create `DNSRecord`s that use a `ClusterAccount` can change the zones that
  account allows. Restrict who may create `DNSRecord`s, or give the account a `zones` list.
- the operator reading Secrets cluster-wide: namespaced `Account`s need it. It reads only the
  Secrets accounts name.
- someone with access to the Simply.com control panel editing or removing the ownership marker.

## Supported versions

Before 1.0, security fixes target the latest release only.
