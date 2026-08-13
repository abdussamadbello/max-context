from ledger import post_transaction as apply_entry


def charge_subscription(account, plan):
    """Bill a subscriber for one period."""
    return apply_entry(account, plan["price"], memo="subscription")


def issue_refund(account, amount):
    return apply_entry(account, -amount, memo="refund")


def apply_late_fee(account):
    return apply_entry(account, 25, memo="late fee")
