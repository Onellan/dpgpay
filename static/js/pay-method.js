(function () {
  function digitsOnly(value) {
    return value.replace(/\D+/g, "");
  }

  const numericOnlyInputs = document.querySelectorAll("[data-numeric-only]");
  numericOnlyInputs.forEach((input) => {
    input.addEventListener("input", function () {
      this.value = digitsOnly(this.value);
    });
  });

  const cardInput = document.querySelector("[data-card-number]");
  if (cardInput) {
    cardInput.addEventListener("input", function () {
      const digits = digitsOnly(this.value).slice(0, 16);
      this.value = digits.replace(/(\d{4})(?=\d)/g, "$1 ");
    });
  }

  const accountInput = document.querySelector("[data-account-number]");
  if (accountInput) {
    accountInput.addEventListener("input", function () {
      this.value = digitsOnly(this.value).slice(0, 20);
    });
  }

  const branchInput = document.querySelector("[data-branch-code]");
  if (branchInput) {
    branchInput.addEventListener("input", function () {
      this.value = digitsOnly(this.value).slice(0, 20);
    });
  }
})();
