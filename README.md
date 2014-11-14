# lxd [![Build Status](https://travis-ci.org/lxc/lxd.svg?branch=master)](https://travis-ci.org/lxc/lxd)

REST API, command line tool and OpenStack integration plugin for LXC.

LXD is pronounced lex-dee.

## Obtaining the code

    go get github.com/lxc/lxd

## Running the tool

    cd $GOPATH/src/github.com/lxc/lxd
    go build

## Bug reports

Bug reports can be filed at https://github.com/lxc/lxd/issues/new

## Contributing

Fixes and new features are greatly appreciated but please read our
[contributing guidelines](CONTRIBUTING.md) first.

Contributions to this project should be sent as pull requests on github.

## Hacking

Sometimes it is useful to view the raw response that LXD sends; you can do
this by:

    wget --no-check-certificate https://127.0.0.1:443/1.0/ping --certificate=/home/tycho/.config/lxd/cert.pem --private-key=/home/tycho/.config/lxd/key.pem -O - -q

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxcontainers](http://webchat.freenode.net/?channels=#lxcontainers) on irc.freenode.net.
