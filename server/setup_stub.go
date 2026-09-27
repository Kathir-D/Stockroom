package main

func cmdSetup(args []string) int   { return notYet("setup") }
func cmdService(args []string) int { return notYet("service") }
func cmdDoctor(args []string) int  { return notYet("doctor") }

func notYet(name string) int { println("stockroom " + name + ": not built yet"); return 1 }
