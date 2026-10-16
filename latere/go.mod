// The Latere platform adapter is a module of its own, so a program that
// imports only the contract never sees it. It builds against the contract in
// this repository (the replace below), which keeps a contract change and the
// adapter it breaks in one commit; a consumer ignores the replace and gets
// the contract version required here or a newer one.
module latere.ai/x/managed-agents/latere

go 1.27.0

require latere.ai/x/managed-agents v0.0.0-20261016014632-945e396cfdbf

replace latere.ai/x/managed-agents => ../
