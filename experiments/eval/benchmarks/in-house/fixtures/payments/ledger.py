"""Core ledger primitives."""


def post_transaction(account, amount, memo=""):
    """Append a double-entry row to the ledger."""
    return {"account": account, "amount": amount, "memo": memo}
