// -*- Mode: Go; indent-tabs-mode: t -*-
//go:build !darwin
// +build !darwin

/*
 * Copyright (C) 2017-2019 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jessevdk/go-flags"

	"github.com/snapcore/snapd/i18n"
	"github.com/snapcore/snapd/snap"
	"github.com/snapcore/snapd/snapdtool"
	"github.com/snapcore/snapd/usersession/agent"
	"github.com/snapcore/snapd/usersession/autostart"
	"github.com/snapcore/snapd/usersession/userd"
)

type cmdUserd struct {
	Autostart bool `long:"autostart"`
	Agent     bool `long:"agent"`
}

var shortUserdHelp = i18n.G("Start the userd service")
var longUserdHelp = i18n.G(`
The userd command starts the snap user session service.
`)

func init() {
	cmd := addCommand("userd",
		shortUserdHelp,
		longUserdHelp,
		func() flags.Commander {
			return &cmdUserd{}
		}, map[string]string{
			// TRANSLATORS: This should not start with a lowercase letter.
			"autostart": i18n.G("Autostart user applications"),
			// TRANSLATORS: This should not start with a lowercase letter.
			"agent": i18n.G("Run the user session agent"),
		}, nil)
	cmd.hidden = true
}

var osChmod = os.Chmod

func maybeFixupUsrSnapPermissions(usrSnapDir string) error {
	// restrict the user's "snap dir", i.e. /home/$USER/snap, to be private with
	// permissions o0700 so that other users cannot read the data there, some
	// snaps such as chromium etc may store secrets inside this directory
	// note that this operation is safe since `userd --autostart` runs as the
	// user so there is no issue with this modification being performed as root,
	// and being vulnerable to symlink switching attacks, etc.
	if err := osChmod(usrSnapDir, 0700); err != nil {
		// if the dir doesn't exist for some reason (i.e. maybe this user has
		// never used snaps but snapd is still installed) then ignore the error
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot restrict user snap home dir %q: %v", usrSnapDir, err)
		}
	}

	return nil
}

func (x *cmdUserd) Execute(args []string) error {
	if len(args) > 0 {
		return ErrExtraArgs
	}

	if x.Autostart {
		// get the user's snap dir ($HOME/snap or $HOME/.snap/data)
		usrSnapDir, err := getUserSnapDir()
		if err != nil {
			return err
		}

		// autostart is called when starting the graphical session, use that as
		// an opportunity to fix ~/snap permission bits
		if err := maybeFixupUsrSnapPermissions(usrSnapDir); err != nil {
			fmt.Fprintf(Stderr, "failure fixing ~/snap permissions: %v\n", err)
		}

		return x.runAutostart(usrSnapDir)
	}

	if x.Agent {
		return x.runAgent()
	}

	return x.runUserd()
}

var signalNotify = signalNotifyImpl

func (x *cmdUserd) runUserd() error {
	var userd userd.Userd
	if err := userd.Init(); err != nil {
		return err
	}
	userd.Start()

	ch, stop := signalNotify(syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case sig := <-ch:
		fmt.Fprintf(Stdout, "Exiting on %s.\n", sig)
	case <-userd.Dying():
		// something called Stop()
	}

	return userd.Stop()
}

func (x *cmdUserd) runAgent() error {
	agent, err := agent.New()
	if err != nil {
		return err
	}
	agent.Version = snapdtool.Version
	agent.Start()

	ch, stop := signalNotify(syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case sig := <-ch:
		fmt.Fprintf(Stdout, "Exiting on %s.\n", sig)
	case <-agent.Dying():
		// something called Stop()
	}

	return agent.Stop()
}

func (x *cmdUserd) runAutostart(usrSnapDir string) error {
	if err := autostart.AutostartSessionApps(usrSnapDir); err != nil {
		return fmt.Errorf("autostart failed for the following apps:\n%v", err)
	}
	return nil
}

func signalNotifyImpl(sig ...os.Signal) (ch chan os.Signal, stop func()) {
	ch = make(chan os.Signal, len(sig))
	signal.Notify(ch, sig...)
	stop = func() { signal.Stop(ch) }
	return ch, stop
}

func getUserSnapDir() (string, error) {
	usr, err := userCurrent()
	if err != nil {
		return "", err
	}

	opts := getSnapDirOptions()
	return snap.SnapDir(usr.HomeDir, opts), nil
}
