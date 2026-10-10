$Env:PGPASSWORD = "123"
$dbs = @(
    'tradedrift_admin',
    'tradedrift_auth',
    'tradedrift_market',
    'tradedrift_matching',
    'tradedrift_notification',
    'tradedrift_order',
    'tradedrift_portfolio',
    'tradedrift_settlement',
    'tradedrift_topup',
    'tradedrift_trade',
    'tradedrift_wallet'
)

foreach ($db in $dbs) {
    Write-Host "Resetting $db..."
    & psql -h localhost -p 5432 -U postgres -d $db -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO postgres; GRANT ALL ON SCHEMA public TO public;"
}
