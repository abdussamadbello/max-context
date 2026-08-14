from ledger import post_transaction as record_payment


def pay_salary(employee, amount):
    """Pay an employee's monthly salary."""
    memo = f"salary:{employee}"
    return record_payment(employee, amount, memo=memo)


def pay_bonus(employee, amount):
    return record_payment(employee, amount, memo="bonus")
