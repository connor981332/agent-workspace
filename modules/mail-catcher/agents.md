### Email (mail-catcher module)

Development email goes to Mailpit, and nothing reaches a real recipient. Send through `$AW_MAIL_SMTP_URL`, whose username tags the mail with this lane. Read this lane's mail at `$AW_MAIL_INBOX_URL`. Other lanes share the catcher, so a check must look only at this lane's tag.
